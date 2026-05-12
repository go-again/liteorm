package gen

import "strings"

// sqlToGo maps an information_schema / pragma SQL type name to a Go type for the
// generated column V. It is deliberately coarse and dialect-tolerant (substring
// matching, all integer widths → int64), so the same type name behaves the same
// regardless of which backend was introspected — re-type the generated struct
// field if you need a narrower type. (The sqlc plugin keeps a separate,
// sqlc-precise map, because there the same name carries an engine-specific
// width.) Note: MySQL bool is tinyint(1), reported as "tinyint", so it maps to
// int64.
func sqlToGo(sqlType string) string {
	t := strings.ToLower(strings.TrimSpace(sqlType))
	switch {
	case strings.Contains(t, "bigint"), strings.Contains(t, "serial"):
		return "int64"
	case strings.Contains(t, "bit"), strings.HasPrefix(t, "bool"):
		return "bool"
	case strings.Contains(t, "int"):
		return "int64"
	case strings.Contains(t, "float"), strings.Contains(t, "double"),
		strings.Contains(t, "real"), strings.Contains(t, "numeric"), strings.Contains(t, "decimal"):
		return "float64"
	case strings.Contains(t, "char"), strings.Contains(t, "text"), strings.Contains(t, "clob"), strings.Contains(t, "uuid"):
		return "string"
	case strings.Contains(t, "blob"), strings.Contains(t, "binary"), strings.Contains(t, "bytea"):
		return "[]byte"
	case strings.Contains(t, "date"), strings.Contains(t, "time"), strings.Contains(t, "timestamp"):
		return "time.Time"
	default:
		return "string"
	}
}
