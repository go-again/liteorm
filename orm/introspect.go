package orm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
)

// ColumnMeta describes an existing database column.
type ColumnMeta struct {
	Name string
	Type string
}

// ColumnInfo is richer live-column metadata than [ColumnMeta], for schema
// browsers and admin tooling (liteorm.org/studio). PKPos is 0 when the column is
// not part of the primary key, else its 1-based position within the key.
type ColumnInfo struct {
	Name    string
	Type    string
	NotNull bool
	Default *string // nil when the column has no default
	PKPos   int
}

// ForeignKey describes one foreign-key column discovered from the live catalog:
// FromColumn (in the referencing table) references RefTable(RefColumn).
type ForeignKey struct {
	FromColumn string
	RefTable   string
	RefColumn  string
}

// IntrospectTables lists the base tables of the connected database via the
// dialect's [dialect.TableLister] capability.
func IntrospectTables(ctx context.Context, sess liteorm.Session) ([]string, error) {
	tl, ok := sess.Dialect().(dialect.TableLister)
	if !ok {
		return nil, fmt.Errorf("orm: dialect %q does not support table listing", sess.Dialect().Name())
	}
	rows, err := sess.QueryContext(ctx, tl.TablesQuery())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IntrospectColumnsFull lists rich column metadata (nullability, default, primary
// key position) via the dialect's [dialect.ColumnIntrospector]. Dialects that do
// not implement it degrade to name+type only (via [IntrospectColumns]).
func IntrospectColumnsFull(ctx context.Context, sess liteorm.Session, table string) ([]ColumnInfo, error) {
	ci, ok := sess.Dialect().(dialect.ColumnIntrospector)
	if !ok {
		cols, err := IntrospectColumns(ctx, sess, table)
		if err != nil {
			return nil, err
		}
		out := make([]ColumnInfo, len(cols))
		for i, c := range cols {
			out[i] = ColumnInfo{Name: c.Name, Type: c.Type}
		}
		return out, nil
	}
	rows, err := sess.QueryContext(ctx, ci.ColumnsFullQuery(table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ColumnInfo
	for rows.Next() {
		var (
			name, typ string
			notnull   int64
			dflt      sql.NullString
			pk        int64
		)
		if err := rows.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		c := ColumnInfo{Name: name, Type: typ, NotNull: notnull != 0, PKPos: int(pk)}
		if dflt.Valid {
			v := dflt.String
			c.Default = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IntrospectAllColumnsFull returns rich column metadata for EVERY base table in a
// single query via the dialect's [dialect.AllColumnsIntrospector], keyed by table
// name — so a schema browser scales to hundreds of tables with one round trip. The
// bool is false (with no error) when the dialect does not implement the capability,
// signalling the caller to fall back to per-table [IntrospectColumnsFull].
func IntrospectAllColumnsFull(ctx context.Context, sess liteorm.Session) (map[string][]ColumnInfo, bool, error) {
	ai, ok := sess.Dialect().(dialect.AllColumnsIntrospector)
	if !ok {
		return nil, false, nil
	}
	rows, err := sess.QueryContext(ctx, ai.AllColumnsFullQuery())
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]ColumnInfo{}
	for rows.Next() {
		var (
			tbl, name, typ string
			notnull        int64
			dflt           sql.NullString
			pk             int64
		)
		if err := rows.Scan(&tbl, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, false, err
		}
		c := ColumnInfo{Name: name, Type: typ, NotNull: notnull != 0, PKPos: int(pk)}
		if dflt.Valid {
			v := dflt.String
			c.Default = &v
		}
		out[tbl] = append(out[tbl], c)
	}
	return out, true, rows.Err()
}

// IntrospectForeignKeys returns the foreign keys of EVERY base table in a single
// query via the dialect's [dialect.ForeignKeyIntrospector], keyed by referencing
// table name. Returns an empty map (no error) when the dialect does not implement
// the capability — foreign-key overlays are additive.
func IntrospectForeignKeys(ctx context.Context, sess liteorm.Session) (map[string][]ForeignKey, error) {
	fi, ok := sess.Dialect().(dialect.ForeignKeyIntrospector)
	if !ok {
		return map[string][]ForeignKey{}, nil
	}
	rows, err := sess.QueryContext(ctx, fi.ForeignKeysQuery())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]ForeignKey{}
	for rows.Next() {
		var tbl, from, refTbl, refCol string
		if err := rows.Scan(&tbl, &from, &refTbl, &refCol); err != nil {
			return nil, err
		}
		out[tbl] = append(out[tbl], ForeignKey{FromColumn: from, RefTable: refTbl, RefColumn: refCol})
	}
	return out, rows.Err()
}

// IntrospectColumns lists the existing columns of table via the dialect's
// Introspector capability. Returns an empty slice if the table does not exist.
func IntrospectColumns(ctx context.Context, sess liteorm.Session, table string) ([]ColumnMeta, error) {
	intro, ok := sess.Dialect().(dialect.Introspector)
	if !ok {
		return nil, fmt.Errorf("orm: dialect %q does not support introspection", sess.Dialect().Name())
	}
	rows, err := sess.QueryContext(ctx, intro.ColumnsQuery(table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ColumnMeta
	for rows.Next() {
		var c ColumnMeta
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IntrospectIndexes lists the names of the indexes on table via the dialect's
// IndexIntrospector capability. It errors when the dialect cannot list indexes.
func IntrospectIndexes(ctx context.Context, sess liteorm.Session, table string) ([]string, error) {
	intro, ok := sess.Dialect().(dialect.IndexIntrospector)
	if !ok {
		return nil, fmt.Errorf("orm: dialect %q does not support index introspection", sess.Dialect().Name())
	}
	rows, err := sess.QueryContext(ctx, intro.IndexesQuery(table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// tryIntrospect lists columns when the dialect supports introspection; it returns
// a non-nil error (used as a signal to fall back to CREATE TABLE) otherwise.
func tryIntrospect(ctx context.Context, sess liteorm.Session, table string) ([]ColumnMeta, error) {
	if _, ok := sess.Dialect().(dialect.Introspector); !ok {
		return nil, fmt.Errorf("orm: no introspector")
	}
	return IntrospectColumns(ctx, sess, table)
}

// ColumnChange is a column whose type the model changed relative to the live
// table. From is the live (catalog) type, To is the model's type. Type changes are
// always reviewable-only — never auto-applied.
type ColumnChange struct {
	Column string
	From   string
	To     string
}

// Changes is a schema diff between the model T and the live table.
type Changes struct {
	Table   string
	Added   []*Field       // in the model, missing in the DB (additive)
	Removed []ColumnMeta   // in the DB, missing from the model (destructive to drop)
	Changed []ColumnChange // present on both sides with a different type (reviewable)
}

// Empty reports whether the model and the live table already agree.
func (c Changes) Empty() bool {
	return len(c.Added) == 0 && len(c.Removed) == 0 && len(c.Changed) == 0
}

// Diff compares the model T against the live table's columns.
func Diff[T any](ctx context.Context, sess liteorm.Session) (Changes, error) {
	s, err := SchemaOf[T]()
	if err != nil {
		return Changes{}, err
	}
	existing, err := IntrospectColumns(ctx, sess, s.Table)
	if err != nil {
		return Changes{}, err
	}
	d := sess.Dialect()
	model := map[string]bool{}
	ch := Changes{Table: s.Table}
	have := map[string]bool{}
	dbType := map[string]string{}
	for _, c := range existing {
		have[strings.ToLower(c.Name)] = true
		dbType[strings.ToLower(c.Name)] = c.Type
	}
	for _, f := range s.Fields {
		key := strings.ToLower(f.Column)
		model[key] = true
		if !have[key] {
			ch.Added = append(ch.Added, f)
			continue
		}
		// Present on both sides — flag a type change (reviewable only, conservative).
		if from := dbType[key]; typeChanged(d.Name(), from, columnType(f, d)) {
			ch.Changed = append(ch.Changed, ColumnChange{Column: f.Column, From: from, To: columnType(f, d)})
		}
	}
	for _, c := range existing {
		if !model[strings.ToLower(c.Name)] {
			ch.Removed = append(ch.Removed, c)
		}
	}
	return ch, nil
}

// GenerateMigration computes the diff for T and returns reviewable up/down SQL —
// it does NOT execute anything (the two-track model: additive changes auto-apply
// via AutoMigrate; destructive ones are emitted here for review). Added columns
// become ADD/DROP; removed columns become a commented destructive DROP.
func GenerateMigration[T any](ctx context.Context, sess liteorm.Session) (up, down string, err error) {
	s, err := SchemaOf[T]()
	if err != nil {
		return "", "", err
	}
	ch, err := Diff[T](ctx, sess)
	if err != nil {
		return "", "", err
	}
	d := sess.Dialect()
	var ups, downs []string
	for _, f := range ch.Added {
		ups = append(ups, addColumnSQL(s.Table, f, d)+";")
		downs = append(downs, dropColumnSQL(s.Table, f.Column, d)+";")
	}
	for _, c := range ch.Removed {
		ups = append(ups, "-- destructive (review before applying): "+dropColumnSQL(s.Table, c.Name, d)+";")
		downs = append(downs, "-- restore column "+c.Name+" ("+c.Type+") manually")
	}
	for _, cc := range ch.Changed {
		ups = append(ups, "-- reviewable type change ("+cc.From+" -> "+cc.To+"): "+alterColumnTypeSQL(d, s.Table, cc.Column, cc.To)+";")
		downs = append(downs, "-- revert "+cc.Column+" to "+cc.From+" manually")
	}
	return strings.Join(ups, "\n"), strings.Join(downs, "\n"), nil
}

// alterColumnTypeSQL renders the dialect's column-type ALTER. It is only ever
// emitted commented-out by GenerateMigration (a type change is never auto-applied);
// SQLite, which can't ALTER a column type in place, gets a manual-rebuild note.
func alterColumnTypeSQL(d dialect.Dialect, table, col, newType string) string {
	q := func(s string) string { return string(d.QuoteIdent(nil, s)) }
	switch d.Name() {
	case "mysql":
		return "ALTER TABLE " + q(table) + " MODIFY COLUMN " + q(col) + " " + newType
	case "mssql":
		return "ALTER TABLE " + q(table) + " ALTER COLUMN " + q(col) + " " + newType
	case "sqlite":
		return "SQLite cannot ALTER a column type in place — rebuild " + table + " to change " + col + " to " + newType
	default: // postgres
		return "ALTER TABLE " + q(table) + " ALTER COLUMN " + q(col) + " TYPE " + newType
	}
}

func addColumnSQL(table string, f *Field, d dialect.Dialect) string {
	var b strings.Builder
	b.WriteString("ALTER TABLE ")
	b.Write(d.QuoteIdent(nil, table))
	// T-SQL is "ALTER TABLE t ADD col type" (no COLUMN keyword).
	if d.Name() == "mssql" {
		b.WriteString(" ADD ")
	} else {
		b.WriteString(" ADD COLUMN ")
	}
	b.Write(d.QuoteIdent(nil, f.Column))
	b.WriteByte(' ')
	b.WriteString(columnType(f, d))
	if f.HasDefault {
		b.WriteString(" DEFAULT ")
		b.WriteString(defaultLiteral(f)) // quote string defaults, matching columnDef (the CREATE TABLE path)
	}
	return b.String()
}

func dropColumnSQL(table, col string, d dialect.Dialect) string {
	var b strings.Builder
	b.WriteString("ALTER TABLE ")
	b.Write(d.QuoteIdent(nil, table))
	b.WriteString(" DROP COLUMN ")
	b.Write(d.QuoteIdent(nil, col))
	return b.String()
}
