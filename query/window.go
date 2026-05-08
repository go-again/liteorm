package query

import (
	"strconv"
	"strings"

	"liteorm.org/dialect"
)

// Window is an OVER (…) specification. Build it with Over() and chain
// PartitionBy / OrderBy, then attach it to a window function with WindowFunc.Over.
type Window struct {
	partition []Field
	order     []OrderTerm
}

// Over starts an empty window specification.
func Over() Window { return Window{} }

// PartitionBy adds PARTITION BY columns to the window.
func (w Window) PartitionBy(cols ...Field) Window {
	w.partition = append(append([]Field{}, w.partition...), cols...)
	return w
}

// OrderBy adds ORDER BY terms to the window (use Asc/Desc).
func (w Window) OrderBy(terms ...OrderTerm) Window {
	w.order = append(append([]OrderTerm{}, w.order...), terms...)
	return w
}

func (w Window) render(d dialect.Dialect) string {
	var parts []string
	if len(w.partition) > 0 {
		ps := make([]string, len(w.partition))
		for i, f := range w.partition {
			ps[i], _ = f.render(d) // partition items are columns; no binds
		}
		parts = append(parts, "PARTITION BY "+strings.Join(ps, ", "))
	}
	if len(w.order) > 0 {
		os := make([]string, len(w.order))
		for i, t := range w.order {
			os[i] = t.renderInto(d)
		}
		parts = append(parts, "ORDER BY "+strings.Join(os, ", "))
	}
	return strings.Join(parts, " ")
}

func (w Window) cols() []string {
	var c []string
	for _, f := range w.partition {
		c = append(c, f.cols...)
	}
	for _, t := range w.order {
		if t.raw == "" {
			c = append(c, t.col)
		}
	}
	return c
}

// WindowFunc is a window function (ROW_NUMBER, RANK, LAG, a running SUM, …)
// awaiting its OVER clause. Attach the window and a result alias with Over to get
// a projection Field for Into.
type WindowFunc struct {
	cols []string
	call func(d dialect.Dialect) string
}

// Over attaches the OVER clause and the result alias, yielding a projection field:
// <func> OVER (<window>) AS <alias>.
func (wf WindowFunc) Over(w Window, alias string) Field {
	cols := append(append([]string{}, wf.cols...), w.cols()...)
	f := plainField(cols, func(d dialect.Dialect) string {
		return wf.call(d) + " OVER (" + w.render(d) + ") AS " + quoteCol(d, alias)
	})
	for _, pf := range w.partition { // surface a bad partition field rather than swallow it
		if pf.err != nil {
			f.err = pf.err
			break
		}
	}
	return f
}

// Ranking window functions.
func RowNumber() WindowFunc {
	return WindowFunc{call: func(dialect.Dialect) string { return "ROW_NUMBER()" }}
}
func Rank() WindowFunc { return WindowFunc{call: func(dialect.Dialect) string { return "RANK()" }} }
func DenseRank() WindowFunc {
	return WindowFunc{call: func(dialect.Dialect) string { return "DENSE_RANK()" }}
}

// Lag / Lead read a column from a row `offset` positions before / after the
// current one within the window.
func Lag[V any](c Column[V], offset int) WindowFunc  { return offsetFunc("LAG", c.name, offset) }
func Lead[V any](c Column[V], offset int) WindowFunc { return offsetFunc("LEAD", c.name, offset) }

func offsetFunc(fn, col string, offset int) WindowFunc {
	return WindowFunc{cols: []string{col}, call: func(d dialect.Dialect) string {
		return fn + "(" + quoteCol(d, col) + ", " + strconv.Itoa(offset) + ")"
	}}
}

// Aggregate window functions (running / partitioned aggregates).
func WindowSum[V any](c Column[V]) WindowFunc   { return aggWindow("SUM", c.name) }
func WindowAvg[V any](c Column[V]) WindowFunc   { return aggWindow("AVG", c.name) }
func WindowCount[V any](c Column[V]) WindowFunc { return aggWindow("COUNT", c.name) }
func WindowMin[V any](c Column[V]) WindowFunc   { return aggWindow("MIN", c.name) }
func WindowMax[V any](c Column[V]) WindowFunc   { return aggWindow("MAX", c.name) }

func aggWindow(fn, col string) WindowFunc {
	return WindowFunc{cols: []string{col}, call: func(d dialect.Dialect) string {
		return fn + "(" + quoteCol(d, col) + ")"
	}}
}

// ScalarSubquery renders a subquery as a single SELECT-list value: (sub) AS alias.
// The subquery must select exactly one column and yield at most one row per outer
// row; correlate it to the outer query with a raw Where referencing the outer
// table's columns. Use it as a projection field in Into — the typed answer to a
// per-row computed value beyond IN/EXISTS. Raw Project remains the escape hatch
// for anything more exotic.
func ScalarSubquery(alias string, sub Subquery) Field {
	return Field{
		err: sub.validateColumns(),
		render: func(d dialect.Dialect) (string, []any) {
			frag, args, _ := sub.fragment(d) // column errors pre-captured in err
			return "(" + frag + ") AS " + quoteCol(d, alias), args
		},
	}
}
