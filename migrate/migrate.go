// Package migrate is liteorm's thin, driver-free migration runner. It applies
// ordered migrations against any liteorm.Session, tracking state in a single-row
// (version, dirty) ledger table (the golang-migrate model) created dialect-aware
// so it works on SQLite/Postgres/MySQL/MSSQL. It does NOT generate DDL — that is
// orm.AutoMigrate / orm.GenerateMigration; this runs the SQL you (or the
// generator) wrote. Load reads three on-disk formats (see source.go). A failed
// migration leaves the ledger dirty; the next run refuses until Force.
package migrate

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

// Migration is one versioned step. Down may be empty (irreversible).
type Migration struct {
	Version uint64
	Name    string
	Up      string
	Down    string
}

// Status is a migration plus whether it has been applied.
type Status struct {
	Version uint64
	Name    string
	Applied bool
}

// DirtyError is returned when the ledger is dirty (a previous migration failed
// part-way). Resolve the database manually, then Force to the correct version.
type DirtyError struct{ Version uint64 }

func (e *DirtyError) Error() string {
	return fmt.Sprintf("migrate: database is dirty at version %d; resolve it and call Force", e.Version)
}

// Migrator applies migrations against a session.
type Migrator struct {
	sess  liteorm.Session
	table string
}

// Option configures a Migrator.
type Option func(*Migrator)

// WithTable overrides the ledger table name (default "schema_migrations").
func WithTable(name string) Option { return func(m *Migrator) { m.table = name } }

// New constructs a Migrator bound to sess.
func New(sess liteorm.Session, opts ...Option) *Migrator {
	m := &Migrator{sess: sess, table: "schema_migrations"}
	for _, o := range opts {
		o(m)
	}
	return m
}

func sortMigs(migs []Migration) {
	slices.SortFunc(migs, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
}

func (m *Migrator) qi(name string) string {
	return string(m.sess.Dialect().QuoteIdent(nil, name))
}

// Version returns the current applied version and whether the ledger is dirty.
func (m *Migrator) Version(ctx context.Context) (uint64, bool, error) {
	if err := m.ensureLedger(ctx); err != nil {
		return 0, false, err
	}
	rows, err := m.sess.QueryContext(ctx, "SELECT version, dirty FROM "+m.qi(m.table))
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var v int64
		var dirtyRaw any
		if err := rows.Scan(&v, &dirtyRaw); err != nil {
			return 0, false, err
		}
		return uint64(v), toBool(dirtyRaw), rows.Err()
	}
	return 0, false, rows.Err()
}

// Up applies every pending migration in version order.
func (m *Migrator) Up(ctx context.Context, migs []Migration) (int, error) {
	return m.UpTo(ctx, migs, ^uint64(0))
}

// UpTo applies pending migrations up to and including target.
func (m *Migrator) UpTo(ctx context.Context, migs []Migration, target uint64) (int, error) {
	sortMigs(migs)
	cur, dirty, err := m.Version(ctx)
	if err != nil {
		return 0, err
	}
	if dirty {
		return 0, &DirtyError{Version: cur}
	}
	applied := 0
	for _, mig := range migs {
		if mig.Version <= cur || mig.Version > target {
			continue
		}
		if err := m.runStep(ctx, mig.Version, mig.Up); err != nil {
			return applied, err
		}
		cur = mig.Version
		applied++
	}
	return applied, nil
}

// Down rolls back the most recently applied migration (one step).
func (m *Migrator) Down(ctx context.Context, migs []Migration) error {
	sortMigs(migs)
	cur, dirty, err := m.Version(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return &DirtyError{Version: cur}
	}
	if cur == 0 {
		return nil
	}
	idx := slices.IndexFunc(migs, func(x Migration) bool { return x.Version == cur })
	if idx < 0 {
		return fmt.Errorf("migrate: no migration found for current version %d", cur)
	}
	mig := migs[idx]
	if mig.Down == "" {
		return fmt.Errorf("migrate: migration %d (%s) is irreversible (no down)", mig.Version, mig.Name)
	}
	prev := uint64(0)
	if idx > 0 {
		prev = migs[idx-1].Version
	}
	if err := m.setVersion(ctx, cur, true); err != nil {
		return err
	}
	for _, stmt := range splitStatements(mig.Down) {
		if _, err := m.sess.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return m.setVersion(ctx, prev, false)
}

// DownTo rolls back every migration with version greater than target.
func (m *Migrator) DownTo(ctx context.Context, migs []Migration, target uint64) error {
	for {
		cur, _, err := m.Version(ctx)
		if err != nil {
			return err
		}
		if cur <= target {
			return nil
		}
		if err := m.Down(ctx, migs); err != nil {
			return err
		}
	}
}

// Status reports each migration and whether it has been applied.
func (m *Migrator) Status(ctx context.Context, migs []Migration) ([]Status, error) {
	sortMigs(migs)
	cur, _, err := m.Version(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(migs))
	for i, mig := range migs {
		out[i] = Status{Version: mig.Version, Name: mig.Name, Applied: mig.Version <= cur}
	}
	return out, nil
}

// Force sets the ledger to version and clears the dirty flag (recovery).
func (m *Migrator) Force(ctx context.Context, version uint64) error {
	if err := m.ensureLedger(ctx); err != nil {
		return err
	}
	return m.setVersion(ctx, version, false)
}

func (m *Migrator) runStep(ctx context.Context, version uint64, up string) error {
	if err := m.setVersion(ctx, version, true); err != nil {
		return err
	}
	for _, stmt := range splitStatements(up) {
		if _, err := m.sess.ExecContext(ctx, stmt); err != nil {
			return err // leaves the ledger dirty at this version
		}
	}
	return m.setVersion(ctx, version, false)
}

func (m *Migrator) setVersion(ctx context.Context, version uint64, dirty bool) error {
	up := sqlgen.Update{
		Table: m.table,
		Set: []sqlgen.SetClause{
			{Column: "version", Arg: int64(version)},
			{Column: "dirty", Arg: dirty},
		},
	}
	q, args, err := up.Build(m.sess.Dialect())
	if err != nil {
		return err
	}
	_, err = m.sess.ExecContext(ctx, q, args...)
	return err
}

func (m *Migrator) ensureLedger(ctx context.Context) error {
	exists, err := m.ledgerExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	d := m.sess.Dialect()
	versionType := d.ColumnType(&dialect.Field{GoType: "int64"})
	boolType := d.ColumnType(&dialect.Field{GoType: "bool"})
	create := "CREATE TABLE " + m.qi(m.table) + " (version " + versionType + " NOT NULL, dirty " + boolType + " NOT NULL)"
	if _, err := m.sess.ExecContext(ctx, create); err != nil {
		return err
	}
	ins := sqlgen.Insert{Table: m.table, Columns: []string{"version", "dirty"}, Rows: [][]any{{int64(0), false}}}
	q, args, err := ins.Build(d)
	if err != nil {
		return err
	}
	_, err = m.sess.ExecContext(ctx, q, args...)
	return err
}

func (m *Migrator) ledgerExists(ctx context.Context) (bool, error) {
	intro, ok := m.sess.Dialect().(dialect.Introspector)
	if !ok {
		return false, nil
	}
	rows, err := m.sess.QueryContext(ctx, intro.ColumnsQuery(m.table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case []byte:
		return len(x) == 1 && x[0] == '1' || string(x) == "true"
	case string:
		return x == "1" || x == "true"
	}
	return false
}
