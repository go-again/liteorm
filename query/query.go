// Package query is liteorm's explicit, generics-first front-end: a typed query
// builder plus a Repo[T] for CRUD, operating against a liteorm.Session (a *DB or
// a transaction) so the same code runs on a connection or inside a tx. It builds
// SQL via internal/sqlgen and maps rows via internal/scan, and imports no
// backend — the driver is wired in at the Session.
//
// Queries are built with typed, column-validated predicates (Filter + Col[V]
// operators, And/Or/Not) for value safety and runtime column checking, with a raw
// Where("frag", args...) escape hatch. Terminals: All, Iter (iter.Seq2),
// First, Count, Exists. Repo[T] has Get/Find/Insert/InsertMany/Update/Delete/
// Upsert; InsertMany uses the backend's BulkInserter (pgx CopyFrom) when present,
// else chunked multi-row VALUES.
package query

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"sync"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
)

type tableNamer interface{ TableName() string }

// tableName derives T's table name: a TableName() method if present, else the
// snake_case of the type name (no pluralization — explicit over implicit).
func tableName[T any]() string {
	var v T
	if tn, ok := any(v).(tableNamer); ok {
		return tn.TableName()
	}
	if tn, ok := any(&v).(tableNamer); ok {
		return tn.TableName()
	}
	return scan.Snake(reflect.TypeFor[T]().Name())
}

// colListKey caches a table-qualified column list per (type, table) — table
// varies because a CTE/subquery aliases the same model under a different name.
type colListKey struct {
	t     reflect.Type
	table string
}

var (
	colListCache sync.Map // colListKey -> []sqlgen.Column (read-only; callers reassign, never mutate elements)
	colSetCache  sync.Map // reflect.Type -> map[string]bool (read-only)
)

func columnsOf[T any](table string) []sqlgen.Column {
	key := colListKey{t: reflect.TypeFor[T](), table: table}
	if v, ok := colListCache.Load(key); ok {
		return v.([]sqlgen.Column)
	}
	cols := scan.Columns[T](false)
	out := make([]sqlgen.Column, len(cols))
	for i, c := range cols {
		out[i] = sqlgen.Column{Table: table, Name: c}
	}
	actual, _ := colListCache.LoadOrStore(key, out)
	return actual.([]sqlgen.Column)
}

// SelectBuilder is a typed SELECT builder. T fixes the result type at compile time.
type SelectBuilder[T any] struct {
	sess         liteorm.Session
	sel          sqlgen.Select
	preds        []Predicate
	orderTerms   []OrderTerm     // typed + raw ORDER BY terms, in call order
	groupFields  []Field         // typed + raw GROUP BY terms, in call order
	distinctOn   []Field         // DISTINCT ON (cols) — Postgres
	requiredFeat dialect.Feature // dialect features the requested clauses need (gated at build)
	buildErr     error           // a deferred construction error (e.g. an invalid UNION arm)
}

// Select starts a typed SELECT over T against sess.
func Select[T any](sess liteorm.Session) *SelectBuilder[T] {
	tbl := tableName[T]()
	return &SelectBuilder[T]{
		sess: sess,
		sel:  sqlgen.Select{Table: tbl, Columns: columnsOf[T](tbl)},
	}
}

// Where adds a raw AND-joined predicate; frag carries positional "?" markers.
// It is the escape hatch; prefer Filter with typed Column predicates.
func (b *SelectBuilder[T]) Where(frag string, args ...any) *SelectBuilder[T] {
	b.sel.Where = append(b.sel.Where, sqlgen.Expr{SQL: frag, Args: args})
	return b
}

// Filter adds typed, column-validated predicates (AND-joined with any Where).
func (b *SelectBuilder[T]) Filter(preds ...Predicate) *SelectBuilder[T] {
	b.preds = append(b.preds, preds...)
	return b
}

// Distinct adds SELECT DISTINCT.
func (b *SelectBuilder[T]) Distinct() *SelectBuilder[T] {
	b.sel.Distinct = true
	return b
}

// GroupBy adds raw GROUP BY terms (emitted verbatim). For typed, validated,
// dialect-quoted columns use GroupByCols; both compose in call order.
func (b *SelectBuilder[T]) GroupBy(cols ...string) *SelectBuilder[T] {
	for _, c := range cols {
		b.groupFields = append(b.groupFields, Expr(c))
	}
	return b
}

// Having adds a raw HAVING predicate (AND-joined); frag carries "?" markers.
func (b *SelectBuilder[T]) Having(frag string, args ...any) *SelectBuilder[T] {
	b.sel.Having = append(b.sel.Having, sqlgen.Expr{SQL: frag, Args: args})
	return b
}

// Join adds a raw join clause, e.g. "JOIN orders ON orders.user_id = users.id".
// The clause may carry "?" markers bound by args (rare in ON, but supported).
func (b *SelectBuilder[T]) Join(clause string, args ...any) *SelectBuilder[T] {
	b.sel.Joins = append(b.sel.Joins, sqlgen.Expr{SQL: clause, Args: args})
	return b
}

// InnerJoin/LeftJoin/RightJoin add a typed join: the table identifier is quoted
// by the dialect, the ON condition is raw SQL (it spans tables) and may carry
// "?" markers bound by args.
func (b *SelectBuilder[T]) InnerJoin(table, on string, args ...any) *SelectBuilder[T] {
	return b.joinKind("INNER JOIN", table, on, args)
}
func (b *SelectBuilder[T]) LeftJoin(table, on string, args ...any) *SelectBuilder[T] {
	return b.joinKind("LEFT JOIN", table, on, args)
}
func (b *SelectBuilder[T]) RightJoin(table, on string, args ...any) *SelectBuilder[T] {
	return b.joinKind("RIGHT JOIN", table, on, args)
}

// CrossJoin adds a CROSS JOIN of the (quoted) table.
func (b *SelectBuilder[T]) CrossJoin(table string) *SelectBuilder[T] {
	return b.Join("CROSS JOIN " + string(b.sess.Dialect().QuoteIdent(nil, table)))
}

func (b *SelectBuilder[T]) joinKind(kind, table, on string, args []any) *SelectBuilder[T] {
	q := string(b.sess.Dialect().QuoteIdent(nil, table))
	return b.Join(kind+" "+q+" ON "+on, args...)
}

// Project overrides the SELECT list with raw column expressions — used to select
// a single column for an IN subquery, or specific columns/aggregates. With no
// Project the full model column set is selected.
func (b *SelectBuilder[T]) Project(cols ...string) *SelectBuilder[T] {
	b.sel.Projection = append(b.sel.Projection, cols...)
	return b
}

// Union appends `other` as a UNION arm (duplicate rows removed). other must
// produce the same column shape as the receiver. The receiver's ORDER BY/LIMIT
// apply to the whole compound.
func (b *SelectBuilder[T]) Union(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.union(other, false)
}

// UnionAll is Union but keeps duplicate rows.
func (b *SelectBuilder[T]) UnionAll(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.union(other, true)
}

func (b *SelectBuilder[T]) union(other *SelectBuilder[T], all bool) *SelectBuilder[T] {
	return b.compound(other, "UNION", all)
}

// OrderBy adds raw ORDER BY terms, e.g. "created_at DESC" (emitted verbatim). For
// typed, validated, dialect-quoted ordering use Order with Asc/Desc; both compose
// in call order.
func (b *SelectBuilder[T]) OrderBy(terms ...string) *SelectBuilder[T] {
	for _, t := range terms {
		b.orderTerms = append(b.orderTerms, OrderExpr(t))
	}
	return b
}

// Limit sets a row limit.
func (b *SelectBuilder[T]) Limit(n int) *SelectBuilder[T] {
	b.sel.Limit, b.sel.HasLimit = n, true
	return b
}

// Offset sets a row offset.
func (b *SelectBuilder[T]) Offset(n int) *SelectBuilder[T] {
	b.sel.Offset, b.sel.HasOffset = n, true
	return b
}

// columnSet returns the model's columns for predicate validation, cached per type
// (the returned map is read-only — callers only test membership).
func columnSet[T any]() map[string]bool {
	t := reflect.TypeFor[T]()
	if v, ok := colSetCache.Load(t); ok {
		return v.(map[string]bool)
	}
	cols := scan.Columns[T](false)
	m := make(map[string]bool, len(cols))
	for _, c := range cols {
		m[c] = true
	}
	actual, _ := colSetCache.LoadOrStore(t, m)
	return actual.(map[string]bool)
}

// resolved returns a copy of the select with typed predicates validated against
// T's columns and rendered into the WHERE list, and the typed ORDER BY / GROUP BY
// terms validated and rendered into their clauses.
func (b *SelectBuilder[T]) resolved() (sqlgen.Select, error) {
	sel := b.sel
	if b.buildErr != nil {
		return sel, b.buildErr
	}
	d := b.sess.Dialect()
	if err := b.checkFeatures(d); err != nil {
		return sel, err
	}
	sel.OrderBy = append([]string{}, b.sel.OrderBy...)
	sel.GroupBy = append([]string{}, b.sel.GroupBy...)
	if len(b.preds) == 0 && len(b.orderTerms) == 0 && len(b.groupFields) == 0 && len(b.distinctOn) == 0 {
		sel.Where = append([]sqlgen.Expr{}, b.sel.Where...)
		return sel, nil
	}
	cols := columnSet[T]()
	where, err := renderPreds[T](b.sel.Where, b.preds, cols, d)
	if err != nil {
		return sel, err
	}
	sel.Where = where
	if err := b.renderClauses(&sel, cols, d); err != nil {
		return sel, err
	}
	return sel, nil
}

// renderPreds appends the typed predicates to a copy of base — each validated
// against the model's columns and rendered for the dialect — returning the
// combined WHERE list. Shared by the Select, Update, and Delete builders.
func renderPreds[T any](base []sqlgen.Expr, preds []Predicate, cols map[string]bool, d dialect.Dialect) ([]sqlgen.Expr, error) {
	out := append([]sqlgen.Expr{}, base...)
	for _, p := range preds {
		if err := checkPred[T](p, cols, d); err != nil {
			return nil, err
		}
		s, a := p.render(d)
		out = append(out, sqlgen.Expr{SQL: s, Args: a})
	}
	return out, nil
}

// checkPred runs the per-predicate validity checks (captured construction error,
// unknown column, unsupported dialect feature). The dialect may be nil to skip
// the feature gate (dialect-independent column validation for subqueries).
func checkPred[T any](p Predicate, cols map[string]bool, d dialect.Dialect) error {
	if p.err != nil {
		return p.err
	}
	for _, c := range p.cols {
		if !cols[c] {
			return fmt.Errorf("query: unknown column %q on table %q", c, tableName[T]())
		}
	}
	if d != nil && p.feat != 0 && !d.Features().Has(p.feat) {
		return fmt.Errorf("query: predicate requires a SQL feature the %s dialect does not support", d.Name())
	}
	return nil
}

// validateColumns runs the dialect-independent column + error checks on this
// builder's predicates. It powers subquery validation (a subquery is validated
// eagerly when it is placed in a predicate, so the outer query reports the error
// before rendering).
func (b *SelectBuilder[T]) validateColumns() error {
	if b.buildErr != nil {
		return b.buildErr
	}
	cols := columnSet[T]()
	for _, p := range b.preds {
		if err := checkPred[T](p, cols, nil); err != nil {
			return err
		}
	}
	return nil
}

// featUnion is the OR of every predicate's required feature, propagated to an
// enclosing subquery predicate so the outer feature gate catches it.
func (b *SelectBuilder[T]) featUnion() dialect.Feature {
	var f dialect.Feature
	for _, p := range b.preds {
		f |= p.feat
	}
	return f
}

// fragment renders this builder as a subquery body using "?" placeholders, so an
// enclosing query renumbers them for its dialect. Identifiers and limits still
// render through the real dialect.
func (b *SelectBuilder[T]) fragment(d dialect.Dialect) (string, []any, error) {
	sel, err := b.resolved()
	if err != nil {
		return "", nil, err
	}
	return sel.Build(qmarkDialect{d})
}

func (b *SelectBuilder[T]) buildSQL() (string, []any, error) {
	sel, err := b.resolved()
	if err != nil {
		return "", nil, err
	}
	return sel.Build(b.sess.Dialect())
}

// All runs the query and returns all rows as typed T.
func (b *SelectBuilder[T]) All(ctx context.Context) ([]T, error) {
	q, args, err := b.buildSQL()
	if err != nil {
		return nil, err
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scan.All[T](rows)
}

// Iter runs the query and streams rows lazily as iter.Seq2[T, error].
func (b *SelectBuilder[T]) Iter(ctx context.Context) iter.Seq2[T, error] {
	q, args, err := b.buildSQL()
	if err != nil {
		return errSeq[T](err)
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return errSeq[T](err)
	}
	return scan.Iter[T](rows)
}

// First returns the first matching row, or liteorm.ErrNoRows.
func (b *SelectBuilder[T]) First(ctx context.Context) (T, error) {
	var zero T
	out, err := b.Limit(1).All(ctx)
	if err != nil {
		return zero, err
	}
	if len(out) == 0 {
		return zero, liteorm.ErrNoRows
	}
	return out[0], nil
}

// Count returns the number of matching rows (ignoring order/limit/offset).
func (b *SelectBuilder[T]) Count(ctx context.Context) (int64, error) {
	sel, err := b.resolved()
	if err != nil {
		return 0, err
	}
	d := b.sess.Dialect()
	// Order/limit/offset and any row lock never affect a count.
	sel.OrderBy, sel.HasLimit, sel.HasOffset, sel.Lock = nil, false, false, nil
	var q string
	var args []any
	if len(sel.Union) > 0 || len(sel.GroupBy) > 0 || sel.Distinct || len(sel.DistinctOn) > 0 {
		// A compound (UNION), grouped, or DISTINCT [ON] query can't be counted by
		// swapping the projection — count over it as a derived table.
		inner, ia, berr := sel.Build(d)
		if berr != nil {
			return 0, berr
		}
		q, args = "SELECT count(*) FROM ("+inner+") AS _cnt", ia
	} else {
		sel.Projection = []string{"count(*)"}
		sel.Columns = nil
		q, args, err = sel.Build(d)
		if err != nil {
			return 0, err
		}
	}
	rows, err := b.sess.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

// Exists reports whether any row matches.
func (b *SelectBuilder[T]) Exists(ctx context.Context) (bool, error) {
	n, err := b.Count(ctx)
	return n > 0, err
}

func errSeq[T any](err error) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var z T
		yield(z, err)
	}
}

// Raw runs a raw SQL query and maps the rows into typed T (the escape hatch).
func Raw[T any](ctx context.Context, sess liteorm.Session, query string, args ...any) ([]T, error) {
	rows, err := sess.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scan.All[T](rows)
}
