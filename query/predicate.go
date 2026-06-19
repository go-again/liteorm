package query

import (
	"strings"

	"liteorm.org/dialect"
)

// Predicate is a typed, dialect-quoted WHERE/HAVING condition built from Column
// operators and the And/Or/Not combinators. The columns it references are
// validated against the model's schema when the predicate is added to a query
// (an unknown column is an error, not silent). This gives compile-time *value*
// safety and runtime *column* validation today; codegen (liteorm gen) will later
// emit the same Column[V] tokens for compile-time column safety too.
type Predicate struct {
	render func(d dialect.Dialect) (sql string, args []any)
	cols   []string
	// feat, when non-zero, is a dialect Feature the predicate requires (e.g.
	// FeatJSONB for jsonb containment, FeatArray for array operators). The query
	// builder rejects the predicate on a dialect that lacks it, so a
	// Postgres-only operator fails loudly at build time rather than as opaque
	// SQL at the database.
	feat dialect.Feature
	// err is a construction-time error (e.g. an invalid subquery) captured so it
	// can surface from the query builder before any SQL is rendered.
	err error
}

// Column is a typed column token. Its operators take values of type V.
type Column[V any] struct {
	name string
	tbl  string // optional table/alias qualifier (for correlated refs); "" = unqualified
}

// Col names a typed column, e.g. Col[string]("email").Eq("a@b.c").
func Col[V any](name string) Column[V] { return Column[V]{name: name} }

// Of qualifies the column with a table name or alias, so it renders
// "table"."col" — for correlating a subquery to its outer query (see EqCol).
func (c Column[V]) Of(table string) Column[V] { c.tbl = table; return c }

func quoteCol(d dialect.Dialect, name string) string {
	return string(d.QuoteIdent(nil, name))
}

// ref renders the column reference, qualified by its table/alias when set.
func (c Column[V]) ref(d dialect.Dialect) string {
	if c.tbl == "" {
		return quoteCol(d, c.name)
	}
	return quoteCol(d, c.tbl) + "." + quoteCol(d, c.name)
}

func (c Column[V]) cmp(op string, v V) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return quoteCol(d, c.name) + " " + op + " ?", []any{v}
		},
		cols: []string{c.name},
	}
}

// Eq/Ne/Gt/Ge/Lt/Le compare the column against a value of its type.
func (c Column[V]) Eq(v V) Predicate { return c.cmp("=", v) }
func (c Column[V]) Ne(v V) Predicate { return c.cmp("<>", v) }
func (c Column[V]) Gt(v V) Predicate { return c.cmp(">", v) }
func (c Column[V]) Ge(v V) Predicate { return c.cmp(">=", v) }
func (c Column[V]) Lt(v V) Predicate { return c.cmp("<", v) }
func (c Column[V]) Le(v V) Predicate { return c.cmp("<=", v) }

// Like matches a SQL LIKE pattern. The pattern is bound verbatim, so its % and _
// are wildcards — use HasPrefix/HasSuffix/Contains when you want a literal needle
// with its wildcards escaped.
func (c Column[V]) Like(pattern string) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return quoteCol(d, c.name) + " LIKE ?", []any{pattern}
		},
		cols: []string{c.name},
	}
}

// HasPrefix/HasSuffix/Contains match a literal substring at the start / end /
// anywhere in the column, escaping any LIKE metacharacters (% and _) in the needle
// so they match literally — HasPrefix("100") will not match "100% off" on the %.
// They render `col LIKE ? ESCAPE '~'` and are portable across all four backends.
func (c Column[V]) HasPrefix(prefix string) Predicate { return c.likeEsc(escapeLike(prefix) + "%") }
func (c Column[V]) HasSuffix(suffix string) Predicate { return c.likeEsc("%" + escapeLike(suffix)) }
func (c Column[V]) Contains(sub string) Predicate     { return c.likeEsc("%" + escapeLike(sub) + "%") }

func (c Column[V]) likeEsc(pattern string) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return quoteCol(d, c.name) + ` LIKE ? ESCAPE '~'`, []any{pattern}
		},
		cols: []string{c.name},
	}
}

// escapeLike escapes the LIKE metacharacters % and _ (and the escape char ~) so a
// user string matches literally under `LIKE ? ESCAPE '~'`. Tilde is the escape
// character (not backslash) because a backslash in a SQL string literal is itself
// special on MySQL — `ESCAPE '~'` is unambiguous on all four dialects.
func escapeLike(s string) string {
	return strings.NewReplacer("~", "~~", "%", "~%", "_", "~_").Replace(s)
}

// EqCol renders a column-to-column equality (c = other) — for correlating a
// subquery to its outer query, e.g. inside an ExistsField subquery:
// Col[int64]("post_id").EqCol(Col[int64]("id").Of("posts")). Only c is validated
// against the (subquery's) model; other references an outer scope and is emitted
// as written, so qualify it with Of.
func (c Column[V]) EqCol(other Column[V]) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return c.ref(d) + " = " + other.ref(d), nil
		},
		cols: []string{c.name},
	}
}

// In / NotIn match membership in a set of values.
func (c Column[V]) In(vs ...V) Predicate    { return c.inList("IN", vs) }
func (c Column[V]) NotIn(vs ...V) Predicate { return c.inList("NOT IN", vs) }

func (c Column[V]) inList(op string, vs []V) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			if len(vs) == 0 {
				// IN () is invalid SQL; an empty set is always-false for IN and
				// always-true for NOT IN.
				if op == "NOT IN" {
					return "1=1", nil
				}
				return "1=0", nil
			}
			var b strings.Builder
			b.WriteString(quoteCol(d, c.name))
			b.WriteByte(' ')
			b.WriteString(op)
			b.WriteString(" (")
			args := make([]any, len(vs))
			for i, v := range vs {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteByte('?')
				args[i] = v
			}
			b.WriteByte(')')
			return b.String(), args
		},
		cols: []string{c.name},
	}
}

// IsNull / IsNotNull test for SQL NULL.
func (c Column[V]) IsNull() Predicate    { return c.nullCheck(" IS NULL") }
func (c Column[V]) IsNotNull() Predicate { return c.nullCheck(" IS NOT NULL") }

func (c Column[V]) nullCheck(suffix string) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return quoteCol(d, c.name) + suffix, nil
		},
		cols: []string{c.name},
	}
}

// And / Or combine predicates; Not negates one.
func And(ps ...Predicate) Predicate { return combine("AND", ps) }
func Or(ps ...Predicate) Predicate  { return combine("OR", ps) }

func Not(p Predicate) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			s, a := p.render(d)
			return "NOT (" + s + ")", a
		},
		cols: p.cols,
		feat: p.feat,
		err:  p.err,
	}
}

func combine(op string, ps []Predicate) Predicate {
	if len(ps) == 1 {
		return ps[0]
	}
	var cols []string
	var feat dialect.Feature
	var firstErr error
	for _, p := range ps {
		cols = append(cols, p.cols...)
		feat |= p.feat
		if firstErr == nil {
			firstErr = p.err
		}
	}
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			if len(ps) == 0 {
				return "1=1", nil // a no-op (always-true) combiner of nothing
			}
			parts := make([]string, len(ps))
			var args []any
			for i, p := range ps {
				s, a := p.render(d)
				parts[i] = s
				args = append(args, a...)
			}
			// Wrap the whole group in one set of parens so it stays atomic when
			// AND-joined with other Where/Filter conditions (a bare `a OR b` would
			// otherwise bind as `… AND a OR b`). Nesting parenthesizes at each level.
			return "(" + strings.Join(parts, " "+op+" ") + ")", args
		},
		cols: cols,
		feat: feat,
		err:  firstErr,
	}
}
