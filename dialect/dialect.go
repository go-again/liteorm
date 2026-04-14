// Package dialect defines the SQL-dialect contract liteorm's core depends on.
// It is driver-free: each backend implements [Dialect]; the internal query
// builder consumes it. Kept deliberately lean (the hot SQL-generation methods
// only); optional SQL capabilities are expressed as [Feature] flags rather than
// interface methods.
package dialect

// Feature is a bitset of optional SQL capabilities a [Dialect] advertises:
// a dialect opts into what it supports instead of growing the interface.
type Feature uint64

const (
	// FeatReturning: RETURNING clause at statement end (Postgres, SQLite).
	FeatReturning Feature = 1 << iota
	// FeatOutput: T-SQL OUTPUT clause instead of RETURNING (MSSQL). Unlike
	// RETURNING (which trails the statement), OUTPUT is positional — after the
	// column list on INSERT, after SET on UPDATE, after the table on DELETE.
	FeatOutput
	// FeatInsertOnConflict: ON CONFLICT (...) DO UPDATE upsert (Postgres, SQLite).
	FeatInsertOnConflict
	// FeatOnDuplicateKey: ON DUPLICATE KEY UPDATE upsert (MySQL).
	FeatOnDuplicateKey
	// FeatMerge: MERGE-based upsert (MSSQL).
	FeatMerge
	// FeatOffsetFetch: OFFSET .. FETCH pagination instead of LIMIT/OFFSET (MSSQL).
	FeatOffsetFetch
	// FeatCTE: WITH common table expressions (incl. recursive). All four dialects
	// support them.
	FeatCTE
	// FeatJSON: native JSON/JSONB column type.
	FeatJSON
	// FeatArray: native array column type (Postgres).
	FeatArray
	// FeatIdentity: IDENTITY/SERIAL/AUTO_INCREMENT autoincrement.
	FeatIdentity
	// FeatLastInsertID: driver returns a usable LastInsertId (SQLite, MySQL).
	FeatLastInsertID
	// FeatJSONB: Postgres binary-JSON containment operators (@>, <@) — narrower
	// than FeatJSON, which only means a native JSON type exists (SQLite/MySQL
	// have JSON but not these operators).
	FeatJSONB
	// FeatRowLocking: SELECT ... FOR UPDATE / FOR SHARE, with optional SKIP LOCKED
	// / NOWAIT (Postgres, MySQL 8). SQLite has no row locks; MSSQL uses table hints
	// instead, so neither advertises this.
	FeatRowLocking
	// FeatDistinctOn: SELECT DISTINCT ON (cols) (Postgres only).
	FeatDistinctOn
	// FeatIntersectExcept: INTERSECT / EXCEPT compound set operators (SQLite,
	// Postgres, MSSQL; MySQL only since 8.0.31, so it is left off there).
	FeatIntersectExcept
	// FeatLateral: LATERAL joins — a joined subquery may reference columns from
	// earlier FROM items (Postgres; MySQL 8.0.14+ uses the same keyword but is left
	// off conservatively).
	FeatLateral
	// FeatUpdateFrom: correlated UPDATE … FROM <source> WHERE … (Postgres, SQLite
	// 3.33+, and T-SQL's UPDATE … FROM on MSSQL). MySQL uses UPDATE … JOIN instead,
	// so it is left off.
	FeatUpdateFrom
)

// Has reports whether f includes all of the given feature bits.
func (f Feature) Has(bits Feature) bool { return f&bits == bits }

// Field describes a planned column for type mapping and DDL generation. Name
// identifies the column; the remaining fields drive ColumnType/AutoIncrement.
type Field struct {
	Name          string
	GoType        string // canonical Go type, e.g. "int64", "string", "time.Time"
	Nullable      bool
	PrimaryKey    bool
	AutoIncrement bool
	Size          int
}

// Introspector is implemented by dialects that can describe an existing table's
// columns. ColumnsQuery returns SQL yielding rows of (name, type). It powers
// auto-migrate's additive column sync and the diff→migration generator.
type Introspector interface {
	ColumnsQuery(table string) string
}

// IndexIntrospector is an optional capability (checked separately from
// Introspector) for listing an existing table's index names. IndexesQuery returns
// SQL yielding one row per index with a single `name` column. It powers
// auto-migrate's additive index sync: an index a model gained is created, but a
// missing index name is simply created — dropping one is left to a reviewable
// migration.
type IndexIntrospector interface {
	IndexesQuery(table string) string
}

// TableLister is an optional capability for enumerating the base tables of the
// connected database (default schema). TablesQuery returns SQL yielding one row
// per table with a single `name` column. It powers schema browsers and admin
// tooling such as liteorm.org/studio.
type TableLister interface {
	TablesQuery() string
}

// ColumnIntrospector is an optional capability returning richer per-column
// metadata than [Introspector]. ColumnsFullQuery returns SQL yielding, per
// column, the FIXED five-column shape:
//
//	name    TEXT     -- column name
//	type    TEXT     -- declared/affinity type
//	notnull INTEGER  -- 1 if NOT NULL, else 0
//	dflt    TEXT     -- default expression, or NULL
//	pk      INTEGER  -- 0 if not part of the primary key, else its 1-based position
//
// so a single driver-neutral scan reads it on every dialect that implements it.
type ColumnIntrospector interface {
	ColumnsFullQuery(table string) string
}

// AllColumnsIntrospector returns rich column metadata for EVERY base table of the
// connected schema in a single query — so a schema browser scales to hundreds of
// tables without one round-trip per table. AllColumnsFullQuery returns SQL
// yielding the [ColumnIntrospector] five-column shape prefixed with the table
// name, the FIXED six-column shape:
//
//	tbl     TEXT     -- table name
//	name    TEXT     -- column name
//	type    TEXT     -- declared/affinity type
//	notnull INTEGER  -- 1 if NOT NULL, else 0
//	dflt    TEXT     -- default expression, or NULL
//	pk      INTEGER  -- 0 if not part of the primary key, else its 1-based position
type AllColumnsIntrospector interface {
	AllColumnsFullQuery() string
}

// ForeignKeyIntrospector returns the foreign keys of EVERY base table of the
// connected schema in a single query (so model-less schema browsers get
// foreign-key navigation without per-table queries). ForeignKeysQuery returns SQL
// yielding one row per foreign-key column, the FIXED four-column shape:
//
//	tbl        TEXT  -- the referencing table
//	from_col   TEXT  -- the referencing column in tbl
//	ref_table  TEXT  -- the referenced table
//	ref_col    TEXT  -- the referenced column
type ForeignKeyIntrospector interface {
	ForeignKeysQuery() string
}

// Dialect is the contract each backend implements. AppendPlaceholder is an
// explicit, position-aware bind-var writer (sqlite/mysql -> "?", pg -> "$n",
// mssql -> "@pN"), so a generics-first builder emits the right placeholder
// directly with no rewrite pass.
type Dialect interface {
	Name() string
	Features() Feature
	// AppendPlaceholder writes the bind var for parameter #n (1-based) into b.
	AppendPlaceholder(b []byte, n int) []byte
	// QuoteIdent writes a safely-quoted identifier, escaping the quote char.
	QuoteIdent(b []byte, ident string) []byte
	// ColumnType returns the SQL column type for a planned column.
	ColumnType(f *Field) string
	// AutoIncrement returns the autoincrement/identity DDL fragment, or "".
	AutoIncrement(f *Field) string
	// DefaultSchema is the implicit schema name ("public"/"main"/"dbo"/"").
	DefaultSchema() string
}
