package orm

import "strings"

// normalizeColumnType canonicalizes a column type — whether emitted by liteorm's
// DDL (e.g. "VARCHAR(255)", "BIGSERIAL") or read back from the catalog (e.g.
// "character varying", "bigint") — into a small comparable vocabulary, so the two
// can be compared without false positives from spelling or size differences. It
// returns known=false for any spelling it doesn't recognize; callers treat an
// unknown type as "no change" (the conservative bias: a missed change is a safe
// reviewable no-op, a false positive churns migrations). Size/precision is dropped
// — this detects type changes (int → text), not width changes (varchar(100) →
// varchar(255)).
func normalizeColumnType(dialectName, raw string) (canonical string, known bool) {
	t := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexByte(t, '('); i >= 0 { // drop "(255)", "(10,2)", "(max)"
		t = strings.TrimSpace(t[:i])
	}
	var m map[string]string
	switch dialectName {
	case "postgres":
		m = postgresTypeCanon
	case "mysql":
		m = mysqlTypeCanon
	case "mssql":
		m = mssqlTypeCanon
	case "sqlite":
		m = sqliteTypeCanon
	default:
		return "", false
	}
	c, ok := m[t]
	return c, ok
}

// Each map folds every spelling liteorm emits AND every spelling the catalog
// returns for the same logical type onto one canonical token.
var (
	postgresTypeCanon = map[string]string{
		"bigint": "int8", "int8": "int8", "bigserial": "int8", "serial8": "int8",
		"integer": "int4", "int": "int4", "int4": "int4", "serial": "int4", "serial4": "int4",
		"smallint": "int2", "int2": "int2",
		"boolean": "bool", "bool": "bool",
		"double precision": "float8", "float8": "float8",
		"real": "float4", "float4": "float4",
		"numeric": "numeric", "decimal": "numeric",
		"text": "text", "character varying": "varchar", "varchar": "varchar", "char": "char", "character": "char",
		"bytea":                    "bytea",
		"timestamp with time zone": "timestamptz", "timestamptz": "timestamptz",
		"timestamp without time zone": "timestamp", "timestamp": "timestamp",
		"date": "date", "time": "time",
		"json": "json", "jsonb": "jsonb", "uuid": "uuid",
	}
	mysqlTypeCanon = map[string]string{
		"bigint": "bigint",
		"int":    "int", "integer": "int",
		"smallint": "smallint", "mediumint": "mediumint",
		"tinyint": "tinyint",
		"double":  "double", "double precision": "double", "real": "double",
		"float":   "float",
		"decimal": "decimal", "numeric": "decimal",
		"varchar": "varchar", "char": "char",
		"text": "text", "tinytext": "text", "mediumtext": "text", "longtext": "text",
		"blob": "blob", "tinyblob": "blob", "mediumblob": "blob", "longblob": "blob",
		"datetime": "datetime", "timestamp": "timestamp", "date": "date", "time": "time",
		"json": "json",
	}
	mssqlTypeCanon = map[string]string{
		"bigint":   "bigint",
		"int":      "int",
		"smallint": "smallint", "tinyint": "tinyint",
		"bit":   "bit",
		"float": "float", "real": "real",
		"decimal": "decimal", "numeric": "decimal",
		"nvarchar": "nvarchar", "varchar": "varchar", "nchar": "nchar", "char": "char",
		"varbinary": "varbinary", "binary": "binary",
		"datetime2": "datetime2", "datetime": "datetime", "date": "date", "time": "time",
		"uniqueidentifier": "uuid",
	}
	sqliteTypeCanon = map[string]string{
		"integer": "integer", "int": "integer",
		"text": "text",
		"real": "real", "double": "real", "float": "real",
		"blob":      "blob",
		"timestamp": "timestamp", "datetime": "timestamp",
		"numeric": "numeric",
	}
)

// typeChanged reports whether a live column's type differs from the model's,
// comparing canonical forms. It is conservative: an unrecognized type on either
// side yields false (no change).
func typeChanged(dialectName, dbType, modelType string) bool {
	a, ka := normalizeColumnType(dialectName, dbType)
	b, kb := normalizeColumnType(dialectName, modelType)
	if !ka || !kb {
		return false
	}
	return a != b
}
