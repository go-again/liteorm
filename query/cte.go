package query

import (
	"slices"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

// inlineSelect is a subquery rendered inline — a CTE body or a derived-table FROM
// source. It reuses the builder's resolved sqlgen.Select so bind placeholders flow
// through the outer statement in one global numbering pass (the pattern UNION uses).
// Any *SelectBuilder[X] satisfies it, so a CTE/derived table can have a different
// row type than the enclosing query.
type inlineSelect interface {
	resolved() (sqlgen.Select, error)
	featUnion() dialect.Feature
}

// With prepends a common table expression named `name` whose body is sub; reference
// it as the FROM source with From(name). Gated by FeatCTE.
func (b *SelectBuilder[T]) With(name string, sub inlineSelect) *SelectBuilder[T] {
	return b.with(name, sub, false)
}

// WithRecursive prepends a recursive CTE (the body's recursive arm refers back to
// `name`, typically via a raw Join). It marks the whole WITH as RECURSIVE.
func (b *SelectBuilder[T]) WithRecursive(name string, sub inlineSelect) *SelectBuilder[T] {
	return b.with(name, sub, true)
}

func (b *SelectBuilder[T]) with(name string, sub inlineSelect, recursive bool) *SelectBuilder[T] {
	sel, err := sub.resolved()
	if err != nil {
		if b.buildErr == nil {
			b.buildErr = err
		}
		return b
	}
	b.requiredFeat |= dialect.FeatCTE | sub.featUnion()
	b.sel.With = append(b.sel.With, sqlgen.CTE{Name: name, Recursive: recursive, Select: sel})
	return b
}

// From overrides the FROM source to `name` — typically a CTE defined with
// With/WithRecursive, but it also works as a plain table-name override. The
// selected columns are re-qualified to `name` (unless a raw Project is set).
func (b *SelectBuilder[T]) From(name string) *SelectBuilder[T] {
	b.sel.Table = name
	b.sel.FromSubquery = nil
	if len(b.sel.Projection) == 0 {
		b.sel.Columns = columnsOf[T](name)
	}
	return b
}

// FromSubquery starts a SELECT whose FROM is the derived table (sub) AS alias. T is
// the row shape the subquery produces; its columns are qualified by alias. Derived
// tables are universally supported, so this is not feature-gated.
func FromSubquery[T any](sess liteorm.Session, alias string, sub inlineSelect) *SelectBuilder[T] {
	b := &SelectBuilder[T]{sess: sess}
	sel, err := sub.resolved()
	if err != nil {
		b.buildErr = err
		return b
	}
	b.requiredFeat |= sub.featUnion()
	b.sel.FromSubquery = &sel
	b.sel.FromAlias = alias
	b.sel.Columns = columnsOf[T](alias)
	return b
}

// JoinSub joins a subquery as a derived table: `<kind> (sub) AS alias ON <on>`,
// where kind is "INNER JOIN" / "LEFT JOIN" / etc. and `on` is raw SQL (it spans
// tables) that may carry "?" markers bound by args.
func (b *SelectBuilder[T]) JoinSub(kind, alias string, sub Subquery, on string, args ...any) *SelectBuilder[T] {
	return b.joinSub(kind, alias, sub, on, false, args)
}

// JoinLateral is JoinSub with the LATERAL keyword, so the subquery may reference
// columns of earlier FROM items. Postgres only (gated by FeatLateral).
func (b *SelectBuilder[T]) JoinLateral(kind, alias string, sub Subquery, on string, args ...any) *SelectBuilder[T] {
	b.requiredFeat |= dialect.FeatLateral
	return b.joinSub(kind, alias, sub, on, true, args)
}

func (b *SelectBuilder[T]) joinSub(kind, alias string, sub Subquery, on string, lateral bool, args []any) *SelectBuilder[T] {
	d := b.sess.Dialect()
	frag, subArgs, err := sub.fragment(d)
	if err != nil {
		if b.buildErr == nil {
			b.buildErr = err
		}
		return b
	}
	b.requiredFeat |= sub.featUnion()
	lat := ""
	if lateral {
		lat = "LATERAL "
	}
	clause := kind + " " + lat + "(" + frag + ") AS " + string(d.QuoteIdent(nil, alias)) + " ON " + on
	allArgs := slices.Concat(subArgs, args)
	b.sel.Joins = append(b.sel.Joins, sqlgen.Expr{SQL: clause, Args: allArgs})
	return b
}
