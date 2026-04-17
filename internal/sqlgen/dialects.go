package sqlgen

import (
	"strconv"
	"strings"

	"liteorm.org/dialect"
)

// sqlLit returns a single-quoted SQL string literal (single quotes doubled).
func sqlLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// The four render-only dialects. Each implements dialect.Dialect. SQLite is the
// dialect wired to a live DB; the other three exist so the builder's correctness
// is proven across placeholder/quote/limit/upsert/returning divergences in
// sqlgen_test.go.
var (
	SQLite   dialect.Dialect = sqliteDialect{}
	Postgres dialect.Dialect = postgresDialect{}
	MySQL    dialect.Dialect = mysqlDialect{}
	MSSQL    dialect.Dialect = mssqlDialect{}
)

// quoteWith quotes ident using the given quote bytes, doubling an embedded
// close byte (so ] -> ]] for brackets, " -> "" for double quotes, and a
// backtick -> two backticks).
func quoteWith(b []byte, openCh, closeCh byte, ident string) []byte {
	b = append(b, openCh)
	for i := 0; i < len(ident); i++ {
		if ident[i] == closeCh {
			b = append(b, closeCh)
		}
		b = append(b, ident[i])
	}
	b = append(b, closeCh)
	return b
}

// --- SQLite ---------------------------------------------------------------

type sqliteDialect struct{}

func (sqliteDialect) Name() string { return "sqlite" }
func (sqliteDialect) Features() dialect.Feature {
	return dialect.FeatReturning | dialect.FeatInsertOnConflict | dialect.FeatCTE |
		dialect.FeatJSON | dialect.FeatLastInsertID | dialect.FeatIntersectExcept |
		dialect.FeatUpdateFrom
}
func (sqliteDialect) AppendPlaceholder(b []byte, _ int) []byte { return append(b, '?') }
func (sqliteDialect) QuoteIdent(b []byte, ident string) []byte { return quoteWith(b, '"', '"', ident) }
func (sqliteDialect) ColumnType(f *dialect.Field) string       { return sqliteType(f.GoType) }
func (sqliteDialect) AutoIncrement(*dialect.Field) string      { return "AUTOINCREMENT" }
func (sqliteDialect) DefaultSchema() string                    { return "main" }
func (sqliteDialect) ColumnsQuery(table string) string {
	return "SELECT name, type FROM pragma_table_info(" + sqlLit(table) + ")"
}
func (sqliteDialect) IndexesQuery(table string) string {
	return "SELECT name FROM pragma_index_list(" + sqlLit(table) + ")"
}
func (sqliteDialect) TablesQuery() string {
	return "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
}
func (sqliteDialect) ColumnsFullQuery(table string) string {
	return `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(` + sqlLit(table) + `)`
}

// AllColumnsFullQuery and ForeignKeysQuery JOIN the table-valued pragma functions
// against sqlite_master so the whole schema's columns / foreign keys come back in
// one query each (no per-table round trips).
func (sqliteDialect) AllColumnsFullQuery() string {
	return `SELECT m.name AS tbl, ti.name, ti.type, ti."notnull", ti.dflt_value, ti.pk
FROM sqlite_master m JOIN pragma_table_info(m.name) ti
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'
ORDER BY m.name, ti.cid`
}
func (sqliteDialect) ForeignKeysQuery() string {
	return `SELECT m.name AS tbl, fk."from" AS from_col, fk."table" AS ref_table, fk."to" AS ref_col
FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) fk
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`
}

func sqliteType(goType string) string {
	switch goType {
	case "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "INTEGER"
	case "float32", "float64":
		return "REAL"
	case "[]byte":
		return "BLOB"
	case "time.Time":
		// A TIMESTAMP-affinity declared type makes modernc/sqlite round-trip
		// time.Time (and sql.NullTime) instead of returning a bare string.
		return "TIMESTAMP"
	default:
		return "TEXT"
	}
}

// --- Postgres -------------------------------------------------------------

type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgres" }
func (postgresDialect) Features() dialect.Feature {
	return dialect.FeatReturning | dialect.FeatInsertOnConflict | dialect.FeatCTE |
		dialect.FeatJSON | dialect.FeatJSONB | dialect.FeatArray | dialect.FeatIdentity |
		dialect.FeatRowLocking | dialect.FeatDistinctOn | dialect.FeatIntersectExcept |
		dialect.FeatLateral | dialect.FeatUpdateFrom
}
func (postgresDialect) AppendPlaceholder(b []byte, n int) []byte {
	b = append(b, '$')
	return strconv.AppendInt(b, int64(n), 10)
}
func (postgresDialect) QuoteIdent(b []byte, ident string) []byte {
	return quoteWith(b, '"', '"', ident)
}
func (postgresDialect) ColumnType(f *dialect.Field) string  { return postgresType(f) }
func (postgresDialect) AutoIncrement(*dialect.Field) string { return "" } // SERIAL/IDENTITY via ColumnType
func (postgresDialect) DefaultSchema() string               { return "public" }
func (postgresDialect) ColumnsQuery(table string) string {
	return "SELECT column_name AS name, data_type AS type FROM information_schema.columns WHERE table_name = " + sqlLit(table)
}
func (postgresDialect) ColumnsFullQuery(table string) string {
	return pgColumnsFull("c.table_name = " + sqlLit(table))
}
func (postgresDialect) AllColumnsFullQuery() string { return pgColumnsFull("") }

// pgColumnsFull builds the rich-column query; with an empty tableFilter it is the
// schema-wide form (table name prefixed onto each row), otherwise the per-table
// form. PK position comes from the table's PRIMARY KEY constraint.
func pgColumnsFull(tableFilter string) string {
	sel := "c.column_name AS name, c.data_type AS type, CASE WHEN c.is_nullable = 'NO' THEN 1 ELSE 0 END AS notnull, c.column_default AS dflt, COALESCE(k.ordinal_position, 0) AS pk"
	where := "c.table_schema = current_schema()"
	order := "c.table_name, c.ordinal_position"
	if tableFilter != "" {
		where += " AND " + tableFilter
		order = "c.ordinal_position"
	} else {
		sel = "c.table_name AS tbl, " + sel
	}
	return `SELECT ` + sel + `
FROM information_schema.columns c
LEFT JOIN information_schema.table_constraints tc ON tc.table_schema = c.table_schema AND tc.table_name = c.table_name AND tc.constraint_type = 'PRIMARY KEY'
LEFT JOIN information_schema.key_column_usage k ON k.constraint_name = tc.constraint_name AND k.table_schema = tc.table_schema AND k.column_name = c.column_name
WHERE ` + where + `
ORDER BY ` + order
}
func (postgresDialect) ForeignKeysQuery() string {
	return `SELECT tc.table_name AS tbl, kcu.column_name AS from_col, ccu.table_name AS ref_table, ccu.column_name AS ref_col
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = current_schema()`
}
func (postgresDialect) IndexesQuery(table string) string {
	return "SELECT indexname AS name FROM pg_indexes WHERE tablename = " + sqlLit(table)
}
func (postgresDialect) TablesQuery() string {
	return "SELECT table_name AS name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name"
}

func postgresType(f *dialect.Field) string {
	if f.AutoIncrement {
		return "BIGSERIAL"
	}
	switch f.GoType {
	case "bool":
		return "BOOLEAN"
	case "int", "int64", "uint", "uint64":
		return "BIGINT"
	case "int8", "int16", "int32", "uint8", "uint16", "uint32":
		return "INTEGER"
	case "float32", "float64":
		return "DOUBLE PRECISION"
	case "[]byte":
		return "BYTEA"
	case "time.Time":
		return "TIMESTAMPTZ"
	default:
		return "TEXT"
	}
}

// --- MySQL ----------------------------------------------------------------

type mysqlDialect struct{}

func (mysqlDialect) Name() string { return "mysql" }
func (mysqlDialect) Features() dialect.Feature {
	return dialect.FeatOnDuplicateKey | dialect.FeatCTE | dialect.FeatJSON |
		dialect.FeatIdentity | dialect.FeatLastInsertID | dialect.FeatRowLocking
}
func (mysqlDialect) AppendPlaceholder(b []byte, _ int) []byte { return append(b, '?') }
func (mysqlDialect) QuoteIdent(b []byte, ident string) []byte { return quoteWith(b, '`', '`', ident) }
func (mysqlDialect) ColumnType(f *dialect.Field) string       { return mysqlType(f) }
func (mysqlDialect) AutoIncrement(*dialect.Field) string      { return "AUTO_INCREMENT" }
func (mysqlDialect) DefaultSchema() string                    { return "" }
func (mysqlDialect) ColumnsQuery(table string) string {
	return "SELECT column_name AS name, data_type AS type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = " + sqlLit(table)
}

// ColumnsFullQuery yields the fixed five-column shape (name, type, notnull, dflt,
// pk) from information_schema, so the studio can introspect an existing MySQL
// database — including primary keys, which it needs to edit/delete rows — with no
// registered models. pk is the column's 1-based position in the PRIMARY key (0 if
// not part of it), read from key_column_usage.
func (mysqlDialect) ColumnsFullQuery(table string) string {
	return `SELECT c.column_name AS name, c.column_type AS type,
	IF(c.is_nullable = 'NO', 1, 0) AS notnull, c.column_default AS dflt,
	COALESCE(k.ordinal_position, 0) AS pk
FROM information_schema.columns c
LEFT JOIN information_schema.key_column_usage k
	ON k.table_schema = c.table_schema AND k.table_name = c.table_name
	AND k.column_name = c.column_name AND k.constraint_name = 'PRIMARY'
WHERE c.table_schema = DATABASE() AND c.table_name = ` + sqlLit(table) + `
ORDER BY c.ordinal_position`
}

// AllColumnsFullQuery / ForeignKeysQuery return every table's columns / foreign
// keys for the connected database in one query each (schema-wide).
func (mysqlDialect) AllColumnsFullQuery() string {
	return `SELECT c.table_name AS tbl, c.column_name AS name, c.column_type AS type,
	IF(c.is_nullable = 'NO', 1, 0) AS notnull, c.column_default AS dflt,
	COALESCE(k.ordinal_position, 0) AS pk
FROM information_schema.columns c
LEFT JOIN information_schema.key_column_usage k
	ON k.table_schema = c.table_schema AND k.table_name = c.table_name
	AND k.column_name = c.column_name AND k.constraint_name = 'PRIMARY'
WHERE c.table_schema = DATABASE()
ORDER BY c.table_name, c.ordinal_position`
}
func (mysqlDialect) ForeignKeysQuery() string {
	return `SELECT table_name AS tbl, column_name AS from_col, referenced_table_name AS ref_table, referenced_column_name AS ref_col
FROM information_schema.key_column_usage
WHERE table_schema = DATABASE() AND referenced_table_name IS NOT NULL`
}
func (mysqlDialect) IndexesQuery(table string) string {
	return "SELECT DISTINCT index_name AS name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = " + sqlLit(table)
}
func (mysqlDialect) TablesQuery() string {
	return "SELECT table_name AS name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name"
}

func mysqlType(f *dialect.Field) string {
	switch f.GoType {
	case "bool":
		return "TINYINT(1)"
	case "int", "int64", "uint", "uint64":
		return "BIGINT"
	case "int8", "int16", "int32", "uint8", "uint16", "uint32":
		return "INT"
	case "float32", "float64":
		return "DOUBLE"
	case "[]byte":
		return "BLOB"
	case "time.Time":
		return "DATETIME"
	default:
		if f.Size > 0 {
			return "VARCHAR(" + strconv.Itoa(f.Size) + ")"
		}
		return "VARCHAR(255)"
	}
}

// --- MSSQL ----------------------------------------------------------------

type mssqlDialect struct{}

func (mssqlDialect) Name() string { return "mssql" }
func (mssqlDialect) Features() dialect.Feature {
	return dialect.FeatOutput | dialect.FeatMerge | dialect.FeatOffsetFetch |
		dialect.FeatIdentity | dialect.FeatIntersectExcept | dialect.FeatCTE |
		dialect.FeatUpdateFrom
}
func (mssqlDialect) AppendPlaceholder(b []byte, n int) []byte {
	b = append(b, "@p"...)
	return strconv.AppendInt(b, int64(n), 10)
}
func (mssqlDialect) QuoteIdent(b []byte, ident string) []byte { return quoteWith(b, '[', ']', ident) }
func (mssqlDialect) ColumnType(f *dialect.Field) string       { return mssqlType(f) }
func (mssqlDialect) AutoIncrement(*dialect.Field) string      { return "IDENTITY(1,1)" }
func (mssqlDialect) DefaultSchema() string                    { return "dbo" }
func (mssqlDialect) ColumnsQuery(table string) string {
	return "SELECT column_name AS name, data_type AS type FROM information_schema.columns WHERE table_name = " + sqlLit(table)
}
func (mssqlDialect) ColumnsFullQuery(table string) string {
	return mssqlColumnsFull("c.object_id = OBJECT_ID(" + sqlLit(table) + ")")
}
func (mssqlDialect) AllColumnsFullQuery() string { return mssqlColumnsFull("") }

// mssqlColumnsFull builds the rich-column query from the sys.* catalog; with an
// empty objFilter it is the schema-wide form, otherwise scoped to one OBJECT_ID.
// PK position comes from the table's primary-key index; default from its default
// constraint.
func mssqlColumnsFull(objFilter string) string {
	sel := "c.name AS name, ty.name AS type, CASE WHEN c.is_nullable = 0 THEN 1 ELSE 0 END AS notnull, dc.definition AS dflt, COALESCE(ic.key_ordinal, 0) AS pk"
	where := "tab.is_ms_shipped = 0"
	order := "tab.name, c.column_id"
	if objFilter != "" {
		where = objFilter
		order = "c.column_id"
	} else {
		sel = "tab.name AS tbl, " + sel
	}
	return `SELECT ` + sel + `
FROM sys.columns c
JOIN sys.tables tab ON tab.object_id = c.object_id
JOIN sys.types ty ON ty.user_type_id = c.user_type_id
LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id
LEFT JOIN sys.indexes i ON i.object_id = c.object_id AND i.is_primary_key = 1
LEFT JOIN sys.index_columns ic ON ic.object_id = c.object_id AND ic.index_id = i.index_id AND ic.column_id = c.column_id
WHERE ` + where + `
ORDER BY ` + order
}
func (mssqlDialect) ForeignKeysQuery() string {
	return `SELECT pt.name AS tbl, pc.name AS from_col, rt.name AS ref_table, rc.name AS ref_col
FROM sys.foreign_key_columns fkc
JOIN sys.tables pt ON pt.object_id = fkc.parent_object_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id`
}
func (mssqlDialect) IndexesQuery(table string) string {
	return "SELECT name FROM sys.indexes WHERE object_id = OBJECT_ID(" + sqlLit(table) + ") AND name IS NOT NULL"
}
func (mssqlDialect) TablesQuery() string {
	return "SELECT name FROM sys.tables ORDER BY name"
}

func mssqlType(f *dialect.Field) string {
	switch f.GoType {
	case "bool":
		return "BIT"
	case "int", "int64", "uint", "uint64":
		return "BIGINT"
	case "int8", "int16", "int32", "uint8", "uint16", "uint32":
		return "INT"
	case "float32", "float64":
		return "FLOAT"
	case "[]byte":
		return "VARBINARY(MAX)"
	case "time.Time":
		return "DATETIME2"
	default:
		if f.Size > 0 {
			return "NVARCHAR(" + strconv.Itoa(f.Size) + ")"
		}
		return "NVARCHAR(255)"
	}
}
