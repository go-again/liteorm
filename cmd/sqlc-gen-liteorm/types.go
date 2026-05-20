package main

import (
	"strings"

	"liteorm.org/cmd/sqlc-gen-liteorm/plugin"
	"liteorm.org/gen"
)

// goType maps a sqlc column to a Go type: the catalog type name to a base type,
// then []T for arrays and *T for nullable scalars. It returns the (possibly
// updated) needTime flag so the emitter knows whether to import time.
func goType(col *plugin.Column, needTime bool) (string, bool) {
	if col == nil {
		return "any", needTime
	}
	base := baseGoType(typeName(col))
	if base == "time.Time" {
		needTime = true
	}
	t := base
	if col.GetIsArray() || col.GetArrayDims() > 0 {
		t = "[]" + t
	}
	if !col.GetNotNull() && t != "[]byte" && !strings.HasPrefix(t, "[]") {
		t = "*" + t
	}
	return t, needTime
}

func typeName(col *plugin.Column) string {
	if col.GetType() != nil {
		return strings.ToLower(col.GetType().GetName())
	}
	return ""
}

// baseGoType maps a database type name (Postgres / MySQL / SQLite spellings) to
// a Go base type. Unknown types fall back to any.
func baseGoType(name string) string {
	switch name {
	case "smallint", "int2", "smallserial", "serial2", "tinyint", "year":
		return "int16"
	case "integer", "int", "int4", "serial", "serial4", "mediumint":
		return "int32"
	case "bigint", "int8", "bigserial", "serial8":
		return "int64"
	case "boolean", "bool":
		return "bool"
	case "real", "float4", "float":
		return "float32"
	case "double precision", "float8", "double":
		return "float64"
	case "numeric", "decimal", "money":
		return "string" // exact decimals; avoid float rounding
	case "text", "varchar", "char", "bpchar", "character varying", "character",
		"name", "citext", "uuid", "longtext", "mediumtext", "tinytext", "enum":
		return "string"
	case "bytea", "blob", "binary", "varbinary", "longblob", "mediumblob", "tinyblob":
		return "[]byte"
	case "timestamp", "timestamptz", "timestamp with time zone",
		"timestamp without time zone", "date", "time", "timetz", "datetime":
		return "time.Time"
	case "json", "jsonb":
		return "[]byte"
	default:
		return "any"
	}
}

// lowerCamel makes a lowerCamelCase Go identifier from a column name: the first
// segment is lowercased, later segments are title-cased (with the shared
// initialism table). Go keywords get a trailing underscore so they stay valid.
func lowerCamel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == ' ' || r == '.' })
	if len(parts) == 0 {
		return "arg"
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(parts[0]))
	for _, p := range parts[1:] {
		b.WriteString(gen.Initialism(p))
	}
	out := b.String()
	if goKeywords[out] {
		out += "_"
	}
	return out
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}
