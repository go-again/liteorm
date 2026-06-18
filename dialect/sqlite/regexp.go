package sqlite

import "gosqlite.org/ext/regexp"

// WhereRegex returns a query-builder WHERE fragment (and its bind args) that
// matches column against an RE2 regular expression. When pattern is left-anchored
// it prepends a GLOB prefix so SQLite can range-scan an index on column and run
// REGEXP only on the survivors; an unanchored pattern falls back to a plain
// REGEXP filter. Pass the result straight to the query builder:
//
//	frag, args := sqlite.WhereRegex("title", `^Intro to .* with Go$`)
//	rows, err := query.Select[Doc](db).Where(frag, args...).All(ctx)
//
// The REGEXP operator must be registered on the connection — blank-import
// gosqlite.org/ext/regexp/auto (gosqlite registers it globally, so it then works
// through liteorm with no further wiring).
func WhereRegex(column, pattern string) (sql string, args []any) {
	prefix := regexp.GlobPrefix(pattern)
	if prefix == "" || prefix == "*" { // unanchored or unusable: plain REGEXP
		return column + " REGEXP ?", []any{pattern}
	}
	return column + " GLOB ? AND " + column + " REGEXP ?", []any{prefix, pattern}
}
