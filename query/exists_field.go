package query

import "liteorm.org/dialect"

// ExistsField projects a correlated EXISTS subquery as a 0/1 result column —
// CASE WHEN EXISTS (<sub>) THEN 1 ELSE 0 END AS "alias" — for use in Into
// alongside Name/Column fields, scanning into a bool. The CASE wrapper (rather
// than a bare EXISTS) is what makes it valid on all four backends, including SQL
// Server, where EXISTS may not appear in a SELECT list. The subquery correlates to
// the outer row with EqCol (or a raw Where):
//
//	hasComment := query.ExistsField("has_comment",
//	    query.Select[Comment](db).Filter(
//	        query.Col[int64]("post_id").EqCol(query.Col[int64]("id").Of("posts"))))
//	rows, _ := query.Into[Post, row](ctx, query.Select[Post](db),
//	    query.Name("id"), query.Name("title"), hasComment)
//
// The subquery validates its own columns; it is not validated against the outer
// model. (A feature-gated predicate inside the subquery is not feature-checked —
// the ordinary Filter path is — so keep dialect-specific operators on the outside.)
func ExistsField[T any](alias string, sub *SelectBuilder[T]) Field {
	return Field{
		err: sub.validateColumns(),
		render: func(d dialect.Dialect) (string, []any) {
			frag, args, _ := sub.fragment(d) // construction error pre-captured in err
			return "CASE WHEN EXISTS (" + frag + ") THEN 1 ELSE 0 END AS " + quoteCol(d, alias), args
		},
	}
}
