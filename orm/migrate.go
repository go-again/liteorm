package orm

import (
	"context"
	"reflect"
	"slices"
	"strings"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
)

// MigrateOption configures AutoMigrate. Options are opt-in; the zero config
// preserves the historical behavior (no foreign-key constraints).
type MigrateOption func(*migrateConfig)

type migrateConfig struct{ foreignKeys bool }

// WithForeignKeys makes AutoMigrate emit a FOREIGN KEY constraint for every
// belongs-to relation on a NEWLY created table (referencing the target's primary
// key). It is off by default — liteorm ships relations as plain columns so additive
// migration and bulk loads stay simple. Adding a constraint to an already-existing
// table is never automatic (it can fail on dirty data); do that with a reviewable
// migration. A single relation can opt in without this global flag via the
// `orm:"constraint:fk"` tag. Migrate referenced tables first, so the target exists
// when the owner's constraint is created.
func WithForeignKeys() MigrateOption { return func(c *migrateConfig) { c.foreignKeys = true } }

// AutoMigrate brings the table for T into being and in sync, additively. It is
// introspection-gated: a non-existent table is CREATEd (with its unique indexes
// and any m2m junction tables); an existing one is synced by ADD COLUMN for
// anything the model gained — it never drops or alters types (those are a
// reviewable migration via GenerateMigration). Iterating the model (never the
// live DB) is what makes "never drop" structural. Unique indexes get the
// soft-delete fix: a partial unique index (... WHERE deleted_at IS NULL) on
// SQLite/Postgres/MSSQL, or a functional unique index on MySQL (which lacks
// partial indexes) — so a soft-deleted row stops occupying the unique key.
func AutoMigrate[T any](ctx context.Context, sess liteorm.Session, opts ...MigrateOption) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	return migrateSchema(ctx, sess, s, buildMigrateConfig(opts))
}

// AutoMigrateAll runs AutoMigrate for several models in one call, in the order
// given — a one-liner for bringing a whole set of tables into sync:
//
//	orm.AutoMigrateAll(ctx, db, Org{}, Member{}, Project{})
//
// Each model is identified by a zero value of its struct type (a value or a
// pointer both work). Migration runs in argument order, so list a referenced table
// before the table that points at it. The options that AutoMigrate[T] takes are
// not applied here (Go has no variadic type parameters to mix them cleanly); when
// you need WithForeignKeys, call the generic AutoMigrate[T] per model.
func AutoMigrateAll(ctx context.Context, sess liteorm.Session, models ...any) error {
	for _, m := range models {
		s, err := SchemaOfType(reflect.TypeOf(m))
		if err != nil {
			return err
		}
		if err := migrateSchema(ctx, sess, s, migrateConfig{}); err != nil {
			return err
		}
	}
	return nil
}

func buildMigrateConfig(opts []MigrateOption) migrateConfig {
	var cfg migrateConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// migrateSchema is the additive, introspection-gated migration for one resolved
// schema — the shared core of AutoMigrate[T] and AutoMigrateAll.
func migrateSchema(ctx context.Context, sess liteorm.Session, s *Schema, cfg migrateConfig) error {
	d := sess.Dialect()
	existing, _ := tryIntrospect(ctx, sess, s.Table)
	if len(existing) == 0 {
		if _, err := sess.ExecContext(ctx, createTableSQL(s, d, foreignKeysFor(s, cfg))); err != nil {
			return err
		}
		for _, stmt := range indexSQL(s, d) {
			if _, err := sess.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	} else {
		have := map[string]bool{}
		for _, c := range existing {
			have[strings.ToLower(c.Name)] = true
		}
		for _, f := range s.Fields {
			if !have[strings.ToLower(f.Column)] {
				if _, err := sess.ExecContext(ctx, addColumnSQL(s.Table, f, d)); err != nil {
					return err
				}
			}
		}
		// Additively sync indexes: create any model-declared index the live table
		// lacks (dropping a removed one is a reviewable migration, never automatic).
		if err := syncIndexes(ctx, sess, s, d); err != nil {
			return err
		}
	}
	// Create any many-to-many junction tables that don't yet exist.
	for _, rel := range s.Relations {
		if rel.Kind != RelManyToMany {
			continue
		}
		if jcols, _ := tryIntrospect(ctx, sess, rel.JoinTable); len(jcols) == 0 {
			if _, err := sess.ExecContext(ctx, createJoinTableSQL(rel, s.PK, d)); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncIndexes creates any model-declared index missing from the live table. It is
// a no-op when the dialect can't list indexes (the capability is optional), so a
// backend without index introspection simply skips this additive step.
func syncIndexes(ctx context.Context, sess liteorm.Session, s *Schema, d dialect.Dialect) error {
	if _, ok := sess.Dialect().(dialect.IndexIntrospector); !ok {
		return nil // optional capability — skip the additive index sync
	}
	names, err := IntrospectIndexes(ctx, sess, s.Table)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(names))
	for _, n := range names {
		live[strings.ToLower(n)] = true
	}
	for _, def := range indexDefs(s, d) {
		if !live[strings.ToLower(def.Name)] {
			if _, err := sess.ExecContext(ctx, def.SQL); err != nil {
				return err
			}
		}
	}
	return nil
}

// foreignKey is a FOREIGN KEY (Column) REFERENCES RefTable(RefColumn) constraint
// emitted (opt-in) into a CREATE TABLE for a belongs-to relation.
type foreignKey struct {
	Column    string
	RefTable  string
	RefColumn string
}

// foreignKeysFor collects the belongs-to relations that should get an FK
// constraint: all of them when WithForeignKeys is set, or individually those
// tagged `constraint:fk`. The result is sorted by column for stable DDL.
func foreignKeysFor(s *Schema, cfg migrateConfig) []foreignKey {
	var out []foreignKey
	for _, rel := range s.Relations {
		if rel.Kind != RelBelongsTo || (!cfg.foreignKeys && !rel.Constraint) {
			continue
		}
		out = append(out, foreignKey{
			Column:    rel.OwnerKey,
			RefTable:  scan.TableNameOf(rel.Target),
			RefColumn: rel.TargetKey,
		})
	}
	slices.SortFunc(out, func(a, b foreignKey) int { return strings.Compare(a.Column, b.Column) })
	return out
}

func createTableSQL(s *Schema, d dialect.Dialect, fks []foreignKey) string {
	// A single PK renders inline ("id INTEGER PRIMARY KEY"); a composite PK renders
	// as a table-level constraint, so the per-column defs omit PRIMARY KEY.
	inlinePK := len(s.PKs) == 1
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.Write(d.QuoteIdent(nil, s.Table))
	b.WriteString(" (\n")
	for i, f := range s.Fields {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		b.WriteString(columnDef(f, d, inlinePK))
	}
	if len(s.PKs) > 1 {
		b.WriteString(",\n  PRIMARY KEY (")
		for i, pk := range s.PKs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.Write(d.QuoteIdent(nil, pk.Column))
		}
		b.WriteByte(')')
	}
	for _, fk := range fks {
		b.WriteString(",\n  FOREIGN KEY (")
		b.Write(d.QuoteIdent(nil, fk.Column))
		b.WriteString(") REFERENCES ")
		b.Write(d.QuoteIdent(nil, fk.RefTable))
		b.WriteString(" (")
		b.Write(d.QuoteIdent(nil, fk.RefColumn))
		b.WriteByte(')')
	}
	b.WriteString("\n)")
	return b.String()
}

// columnDef renders one column definition, ordering the autoincrement keyword
// per dialect: SQLite wants "INTEGER PRIMARY KEY AUTOINCREMENT" (after PK), while
// MySQL/MSSQL want the keyword before PRIMARY KEY ("BIGINT IDENTITY(1,1) PRIMARY
// KEY"). Postgres encodes it in the type (BIGSERIAL) so AutoIncrement is empty.
func columnDef(f *Field, d dialect.Dialect, inlinePK bool) string {
	var b strings.Builder
	b.Write(d.QuoteIdent(nil, f.Column))
	b.WriteByte(' ')
	b.WriteString(columnType(f, d))
	auto := ""
	if f.Auto {
		auto = d.AutoIncrement(&f.dialField)
	}
	pk := f.PK && inlinePK // composite-PK columns render their key as a table-level constraint
	if d.Name() == "sqlite" {
		if pk {
			b.WriteString(" PRIMARY KEY")
		}
		if auto != "" {
			b.WriteByte(' ')
			b.WriteString(auto)
		}
	} else {
		if auto != "" {
			b.WriteByte(' ')
			b.WriteString(auto)
		}
		if pk {
			b.WriteString(" PRIMARY KEY")
		}
	}
	if f.NotNull && !pk {
		b.WriteString(" NOT NULL")
	}
	if f.HasDefault {
		b.WriteString(" DEFAULT ")
		b.WriteString(defaultLiteral(f))
	}
	if f.Check != "" {
		b.WriteString(" CHECK (")
		b.WriteString(f.Check)
		b.WriteByte(')')
	}
	return b.String()
}

// defaultLiteral renders a column's DEFAULT value for DDL. A string/text column
// gets its default quoted as a SQL string literal — matching how gorm treats a
// bare `default:` on a string field (e.g. gorm `default:user` or `default:#fff`),
// so models ported from gorm migrate without rewriting their tags. The value is
// left verbatim when it is already a quoted literal or a parenthesized
// expression (the escape hatch for a SQL expression default), and for non-string
// columns (numbers, booleans, CURRENT_TIMESTAMP, …).
func defaultLiteral(f *Field) string {
	v := f.Default
	if f.dialField.GoType != "string" {
		return v
	}
	if v == "" {
		return "''"
	}
	if v[0] == '\'' || v[0] == '(' {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func columnType(f *Field, d dialect.Dialect) string {
	if f.sqlType != "" {
		return f.sqlType
	}
	return d.ColumnType(&f.dialField)
}

// indexDef is one model-declared index: its name (used to detect whether the live
// table already has it) and the CREATE statement that builds it.
type indexDef struct {
	Name string
	SQL  string
}

// indexSQL returns just the CREATE statements (the table-creation path runs them all).
func indexSQL(s *Schema, d dialect.Dialect) []string {
	defs := indexDefs(s, d)
	out := make([]string, len(defs))
	for i, def := range defs {
		out[i] = def.SQL
	}
	return out
}

// indexDefs returns every index the model declares (unique first, then non-unique
// secondary indexes), each paired with its name so the additive sync can create
// only the ones the live table is missing.
func indexDefs(s *Schema, d dialect.Dialect) []indexDef {
	var out []indexDef
	for _, f := range s.Fields {
		if !f.Unique {
			continue
		}
		name := "ux_" + s.Table + "_" + f.Column
		var b strings.Builder
		b.WriteString("CREATE UNIQUE INDEX ")
		b.Write(d.QuoteIdent(nil, name))
		b.WriteString(" ON ")
		b.Write(d.QuoteIdent(nil, s.Table))
		switch {
		case s.SoftDelete != nil && d.Name() == "mysql":
			// MySQL has no partial index; a functional index returning NULL for
			// soft-deleted rows excludes them from uniqueness (NULLs are distinct).
			b.WriteString(" ((CASE WHEN ")
			b.Write(d.QuoteIdent(nil, s.SoftDelete.Column))
			b.WriteString(" IS NULL THEN ")
			b.Write(d.QuoteIdent(nil, f.Column))
			b.WriteString(" ELSE NULL END))")
		case s.SoftDelete != nil:
			// Partial/filtered unique index (SQLite, Postgres, MSSQL).
			b.WriteString(" (")
			b.Write(d.QuoteIdent(nil, f.Column))
			b.WriteString(") WHERE ")
			b.Write(d.QuoteIdent(nil, s.SoftDelete.Column))
			b.WriteString(" IS NULL")
		default:
			b.WriteString(" (")
			b.Write(d.QuoteIdent(nil, f.Column))
			b.WriteByte(')')
		}
		out = append(out, indexDef{Name: name, SQL: b.String()})
	}
	// Non-unique secondary indexes (orm/gorm "index").
	for _, f := range s.Fields {
		if !f.HasIndex || f.Unique { // a unique field already has an index above
			continue
		}
		name := f.IndexName
		if name == "" {
			name = "ix_" + s.Table + "_" + f.Column
		}
		var b strings.Builder
		b.WriteString("CREATE INDEX ")
		b.Write(d.QuoteIdent(nil, name))
		b.WriteString(" ON ")
		b.Write(d.QuoteIdent(nil, s.Table))
		b.WriteString(" (")
		b.Write(d.QuoteIdent(nil, f.Column))
		b.WriteByte(')')
		out = append(out, indexDef{Name: name, SQL: b.String()})
	}
	return out
}

func createJoinTableSQL(rel *Relation, ownerPK *Field, d dialect.Dialect) string {
	targetPK := pkField(rel.Target)
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.Write(d.QuoteIdent(nil, rel.JoinTable))
	b.WriteString(" (\n  ")
	b.Write(d.QuoteIdent(nil, rel.OwnerFK))
	b.WriteByte(' ')
	b.WriteString(fkColumnType(ownerPK, d))
	b.WriteString(" NOT NULL,\n  ")
	b.Write(d.QuoteIdent(nil, rel.TargetFK))
	b.WriteByte(' ')
	b.WriteString(fkColumnType(targetPK, d))
	b.WriteString(" NOT NULL,\n  PRIMARY KEY (")
	b.Write(d.QuoteIdent(nil, rel.OwnerFK))
	b.WriteString(", ")
	b.Write(d.QuoteIdent(nil, rel.TargetFK))
	b.WriteString(")\n)")
	return b.String()
}

// fkColumnType returns the non-autoincrement column type matching a referenced
// primary key (e.g. int64 PK -> BIGINT, not BIGSERIAL).
func fkColumnType(pk *Field, d dialect.Dialect) string {
	if pk == nil {
		return d.ColumnType(&dialect.Field{GoType: "int64"})
	}
	return d.ColumnType(&dialect.Field{GoType: pk.dialField.GoType})
}
