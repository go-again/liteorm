package query

import "liteorm.org/dialect"

// Subquery is a SELECT usable inside a predicate (IN / NOT IN / EXISTS). Any
// *SelectBuilder[T] satisfies it; build it like a normal query and (for IN) use
// Project to select the single matched column. The subquery's columns are
// validated when it is placed in a predicate, so an error surfaces from the
// outer query's terminal before any SQL runs.
type Subquery interface {
	fragment(d dialect.Dialect) (string, []any, error)
	validateColumns() error
	featUnion() dialect.Feature
}

// qmarkDialect wraps a dialect so bind parameters render as "?" instead of the
// dialect's native placeholder. A subquery renders through it; the enclosing
// query then renumbers every "?" (its own and the subquery's) in one pass, so
// Postgres still gets $1,$2,… in order.
type qmarkDialect struct{ dialect.Dialect }

func (qmarkDialect) AppendPlaceholder(b []byte, _ int) []byte { return append(b, '?') }

// InQuery renders `col IN (subquery)`; the subquery must Project one column.
func (c Column[V]) InQuery(sub Subquery) Predicate { return c.inSub("IN", sub) }

// NotInQuery renders `col NOT IN (subquery)`.
func (c Column[V]) NotInQuery(sub Subquery) Predicate { return c.inSub("NOT IN", sub) }

func (c Column[V]) inSub(op string, sub Subquery) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			f, a, _ := sub.fragment(d) // error pre-captured in err below
			return quoteCol(d, c.name) + " " + op + " (" + f + ")", a
		},
		cols: []string{c.name},
		feat: sub.featUnion(),
		err:  sub.validateColumns(),
	}
}

// Exists renders an `EXISTS (subquery)` predicate (use it inside Filter). It is
// distinct from the SelectBuilder.Exists terminal, which executes the query and
// returns a bool. The subquery typically correlates to the outer query via a raw
// Where, e.g.
//
//	query.Select[User](db).Filter(query.Exists(
//	    query.Select[Order](db).Project("1").Where("orders.user_id = users.id")))
func Exists[T any](sub *SelectBuilder[T]) Predicate { return existsPred("EXISTS", sub) }

// NotExists renders `NOT EXISTS (subquery)`.
func NotExists[T any](sub *SelectBuilder[T]) Predicate { return existsPred("NOT EXISTS", sub) }

func existsPred(op string, sub Subquery) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			f, a, _ := sub.fragment(d)
			return op + " (" + f + ")", a
		},
		feat: sub.featUnion(),
		err:  sub.validateColumns(),
	}
}
