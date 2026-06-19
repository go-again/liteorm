package sqlite

import "liteorm.org/query"

// RowidCol is a typed column token for SQLite's implicit "rowid" — the 64-bit
// row key every ordinary table has (also reachable as "oid"/"_rowid_"). It is an
// Unvalidated column, so it passes the model-schema validation that would
// otherwise reject "rowid" as an undeclared field. Use it anywhere a Column[int64]
// is accepted — Filter, Order, Pluck:
//
//	query.Select[Item](db).Order(query.Asc(sqlite.RowidCol()))
//	query.Pluck[Item, int64](ctx, b, sqlite.RowidCol())
//	query.Select[Item](db).Filter(sqlite.RowidCol().Gt(lastSeen))
//
// On a table whose primary key is an INTEGER PRIMARY KEY, "rowid" is an alias of
// that PK column — so a Rowid projection reports the PK column's name, and you'd
// scan it through the PK field. Rowid is most useful on tables whose key is not
// an integer PK (a string-keyed or WITHOUT ROWID-adjacent model), where it is a
// distinct implicit column.
func RowidCol() query.Column[int64] { return query.Col[int64]("rowid").Unvalidated() }

// Rowid is the "rowid" column as a projection Field, for selecting the implicit
// key alongside model columns in a query.Into projection without a raw fragment:
//
//	query.Into[Item, reindexRow](ctx, b, sqlite.Rowid(), query.Name("title"))
//
// It is the typed, dialect-quoted counterpart of query.Expr("rowid").
func Rowid() query.Field { return RowidCol().Field() }
