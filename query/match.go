package query

import "liteorm.org/dialect"

// Match builds a SQLite MATCH predicate — `col MATCH ?` — for full-text (FTS5),
// spellfix1, and sqlite-vec virtual tables, composable inside Filter / And / Or
// alongside ordinary column predicates. It is gated on dialect.FeatMatch: on a
// non-SQLite backend the query fails loudly at build time rather than emitting
// unsupported SQL. (Postgres and MySQL full-text use different operators; their
// predicates belong under their own dialects.)
//
//	hit, err := orm.NewRepo[Vocab](db).
//	    Filter(query.Match("word", term), query.Col[int]("scope").Le(2)).
//	    OrderBy("distance ASC").
//	    First(ctx)
func Match(col, q string) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return quoteCol(d, col) + " MATCH ?", []any{q}
		},
		cols: []string{col},
		feat: dialect.FeatMatch,
	}
}
