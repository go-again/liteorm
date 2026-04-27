package query

import (
	"context"
	"database/sql"
	"fmt"

	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
)

// OrderTerm is one ORDER BY element. Build a typed, column-validated term with
// Asc/Desc (the column is quoted by the dialect), or a raw fragment with
// OrderExpr (the escape hatch). Pass terms to SelectBuilder.Order.
type OrderTerm struct {
	col string // column to quote; empty when raw is set
	dir string // "ASC" / "DESC" / ""
	raw string // a raw ORDER BY fragment, emitted verbatim
}

// Asc orders by a typed column ascending; the column is validated against the
// model and quoted by the dialect.
func Asc[V any](c Column[V]) OrderTerm { return OrderTerm{col: c.name, dir: "ASC"} }

// Desc orders by a typed column descending.
func Desc[V any](c Column[V]) OrderTerm { return OrderTerm{col: c.name, dir: "DESC"} }

// OrderExpr is the raw escape hatch for an ORDER BY term the typed helpers can't
// express (a function, a collation, NULLS FIRST). It is emitted verbatim.
func OrderExpr(raw string) OrderTerm { return OrderTerm{raw: raw} }

func (t OrderTerm) renderInto(d dialect.Dialect) string {
	if t.raw != "" {
		return t.raw
	}
	s := quoteCol(d, t.col)
	if t.dir != "" {
		s += " " + t.dir
	}
	return s
}

// Order adds typed ORDER BY terms (Asc/Desc/OrderExpr), validated and quoted. It
// is the typed counterpart of the raw OrderBy(...string); both append to the same
// ordered list, so they compose in call order.
func (b *SelectBuilder[T]) Order(terms ...OrderTerm) *SelectBuilder[T] {
	b.orderTerms = append(b.orderTerms, terms...)
	return b
}

// Field is a value-type-erased reference to something selectable or groupable —
// a column whose Go type is irrelevant in this position (GROUP BY, a typed
// projection). Build one from a typed column with Column.Field, by name with
// Name, a raw expression with Expr, or an aggregate with SumAs/AvgAs/MinAs/MaxAs/
// CountAs. The referenced columns are validated against the model.
type Field struct {
	cols   []string
	err    error // a construction error (e.g. an invalid scalar subquery), surfaced by Into
	render func(d dialect.Dialect) (string, []any)
}

// plainField makes a Field that renders s with no binds.
func plainField(cols []string, render func(d dialect.Dialect) string) Field {
	return Field{cols: cols, render: func(d dialect.Dialect) (string, []any) { return render(d), nil }}
}

// Field erases a typed column for a clause position; it is quoted by the dialect.
func (c Column[V]) Field() Field {
	return plainField([]string{c.name}, func(d dialect.Dialect) string { return quoteCol(d, c.name) })
}

// Name references a column by name (quoted by the dialect, validated against the
// model) for a clause position.
func Name(col string) Field {
	return plainField([]string{col}, func(d dialect.Dialect) string { return quoteCol(d, col) })
}

// Expr is the raw escape hatch for a selectable/groupable expression; it is
// emitted verbatim and not column-validated.
func Expr(raw string) Field {
	return plainField(nil, func(dialect.Dialect) string { return raw })
}

func aggField[V any](fn string, c Column[V], alias string) Field {
	return plainField([]string{c.name}, func(d dialect.Dialect) string {
		return fn + "(" + quoteCol(d, c.name) + ") AS " + quoteCol(d, alias)
	})
}

// SumAs/AvgAs/MinAs/MaxAs/CountAs are aggregate projection items (e.g. for a
// grouped query): each renders AGG(col) AS alias and validates col. Use them in
// Into alongside the grouped columns.
func SumAs[V any](c Column[V], alias string) Field   { return aggField("SUM", c, alias) }
func AvgAs[V any](c Column[V], alias string) Field   { return aggField("AVG", c, alias) }
func MinAs[V any](c Column[V], alias string) Field   { return aggField("MIN", c, alias) }
func MaxAs[V any](c Column[V], alias string) Field   { return aggField("MAX", c, alias) }
func CountAs[V any](c Column[V], alias string) Field { return aggField("COUNT", c, alias) }

// GroupByCols adds typed GROUP BY terms (validated + quoted), the typed
// counterpart of the raw GroupBy(...string). Both append to the same list.
func (b *SelectBuilder[T]) GroupByCols(fields ...Field) *SelectBuilder[T] {
	b.groupFields = append(b.groupFields, fields...)
	return b
}

// renderClauses validates and renders the typed ORDER BY / GROUP BY terms into the
// sel, against T's columns. It is called from resolved after predicates.
// unknownCol returns an error naming the first of cols not present in known.
func unknownCol[T any](known map[string]bool, cols ...string) error {
	for _, c := range cols {
		if !known[c] {
			return fmt.Errorf("query: unknown column %q on table %q", c, tableName[T]())
		}
	}
	return nil
}

// clauseField renders a Field used in a GROUP BY / DISTINCT ON position (a
// []string clause that can't carry binds): it surfaces the field's construction
// error and rejects a parameterized expression (e.g. a scalar subquery) rather
// than silently emitting a placeholder with no argument.
func clauseField[T any](f Field, what string, cols map[string]bool, d dialect.Dialect) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if err := unknownCol[T](cols, f.cols...); err != nil {
		return "", err
	}
	s, args := f.render(d)
	if len(args) > 0 {
		return "", fmt.Errorf("query: a parameterized expression is not supported in %s", what)
	}
	return s, nil
}

func (b *SelectBuilder[T]) renderClauses(sel *sqlgen.Select, cols map[string]bool, d dialect.Dialect) error {
	for _, f := range b.distinctOn {
		s, err := clauseField[T](f, "DISTINCT ON", cols, d)
		if err != nil {
			return err
		}
		sel.DistinctOn = append(sel.DistinctOn, s)
	}
	for _, f := range b.groupFields {
		s, err := clauseField[T](f, "GROUP BY", cols, d)
		if err != nil {
			return err
		}
		sel.GroupBy = append(sel.GroupBy, s)
	}
	for _, t := range b.orderTerms {
		if t.raw == "" {
			if err := unknownCol[T](cols, t.col); err != nil {
				return err
			}
		}
		sel.OrderBy = append(sel.OrderBy, t.renderInto(d))
	}
	return nil
}

// Into runs b projected onto the given fields and scans each row into R (a result
// struct whose columns match the projection's names/aliases). It is the typed
// projection terminal — column-validated, dialect-quoted — and pairs with
// GroupByCols + the AggAs helpers for grouped aggregates. For a fully custom shape,
// Raw[R] remains the escape hatch.
func Into[T any, R any](ctx context.Context, b *SelectBuilder[T], fields ...Field) ([]R, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("query: Into requires at least one projection field")
	}
	cols := columnSet[T]()
	for _, f := range fields {
		if f.err != nil {
			return nil, f.err
		}
		if err := unknownCol[T](cols, f.cols...); err != nil {
			return nil, err
		}
	}
	sel, err := b.resolved()
	if err != nil {
		return nil, err
	}
	d := b.sess.Dialect()
	sel.Columns = nil
	sel.ProjectionExprs = make([]sqlgen.Expr, len(fields))
	for i, f := range fields {
		s, a := f.render(d)
		sel.ProjectionExprs[i] = sqlgen.Expr{SQL: s, Args: a}
	}
	q, args, err := sel.Build(d)
	if err != nil {
		return nil, err
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scan.All[R](rows)
}

// Pluck projects a single typed column and returns its values as []V — the
// column-validated, dialect-quoted equivalent of gorm's Pluck / xorm's Cols, for
// the common "give me all the emails / ids" read without allocating full rows.
// It honors the builder's filters/order/limit.
func Pluck[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) ([]V, error) {
	f := col.Field()
	if f.err != nil {
		return nil, f.err
	}
	if err := unknownCol[T](columnSet[T](), f.cols...); err != nil {
		return nil, err
	}
	sel, err := b.resolved()
	if err != nil {
		return nil, err
	}
	d := b.sess.Dialect()
	sel.Columns = nil
	s, a := f.render(d)
	sel.ProjectionExprs = []sqlgen.Expr{{SQL: s, Args: a}}
	q, args, err := sel.Build(d)
	if err != nil {
		return nil, err
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scan.Scalars[V](rows)
}

// Sum/Avg/Min/Max/CountCol are whole-set aggregate terminals: each builds
// SELECT AGG(col) over b's filters/joins (ignoring order/limit) and returns the
// scalar. NULL (e.g. an aggregate over no rows) returns R's zero value. For
// grouped aggregates use Into with the AggAs helpers.
func Sum[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) (V, error) {
	return scalarAgg[T, V](ctx, b, "SUM", col.name)
}
func Avg[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) (float64, error) {
	return scalarAgg[T, float64](ctx, b, "AVG", col.name)
}
func Min[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) (V, error) {
	return scalarAgg[T, V](ctx, b, "MIN", col.name)
}
func Max[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) (V, error) {
	return scalarAgg[T, V](ctx, b, "MAX", col.name)
}
func CountCol[T any, V any](ctx context.Context, b *SelectBuilder[T], col Column[V]) (int64, error) {
	return scalarAgg[T, int64](ctx, b, "COUNT", col.name)
}

func scalarAgg[T any, R any](ctx context.Context, b *SelectBuilder[T], fn, col string) (R, error) {
	var zero R
	if err := unknownCol[T](columnSet[T](), col); err != nil {
		return zero, err
	}
	sel, err := b.resolved()
	if err != nil {
		return zero, err
	}
	// A whole-set aggregate can't combine with grouping, DISTINCT ON, or a set
	// operation — use Into for grouped aggregates rather than emit wrong SQL.
	if len(sel.GroupBy) > 0 || len(sel.DistinctOn) > 0 || len(sel.Union) > 0 {
		return zero, fmt.Errorf("query: %s is a whole-set aggregate and cannot combine with GroupBy/DistinctOn/set operations — use Into for grouped aggregates", fn)
	}
	d := b.sess.Dialect()
	sel.OrderBy, sel.HasLimit, sel.HasOffset, sel.Lock = nil, false, false, nil
	sel.Columns = nil
	sel.Projection = []string{fn + "(" + quoteCol(d, col) + ")"}
	q, args, err := sel.Build(d)
	if err != nil {
		return zero, err
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return zero, err
	}
	defer func() { _ = rows.Close() }()
	var nv sql.Null[R]
	if rows.Next() {
		if err := rows.Scan(&nv); err != nil {
			return zero, err
		}
	}
	return nv.V, rows.Err()
}
