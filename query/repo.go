package query

import (
	"context"
	"fmt"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
)

// Repo is a typed repository for CRUD over T against a liteorm.Session.
type Repo[T any] struct {
	sess liteorm.Session
}

// NewRepo constructs a Repo[T] bound to sess (a *DB or a transaction).
func NewRepo[T any](sess liteorm.Session) *Repo[T] { return &Repo[T]{sess: sess} }

// Insert inserts v, reading the generated primary key back via RETURNING (or a
// plain exec where the dialect lacks it). v is updated in place on RETURNING.
func (r *Repo[T]) Insert(ctx context.Context, v *T) error {
	cols := scan.Columns[T](true) // omit auto-increment PK
	ins := sqlgen.Insert{
		Table:   tableName[T](),
		Columns: cols,
		Rows:    [][]any{scan.Values(v, cols)},
	}
	return InsertCapturingPK(ctx, r.sess, ins, v)
}

// InsertMany inserts vs in bulk: the backend's BulkInserter (pgx native CopyFrom)
// when available, else chunked multi-row VALUES. It does not read primary keys
// back (use Insert per row when you need the generated id).
func (r *Repo[T]) InsertMany(ctx context.Context, vs []T) error {
	if len(vs) == 0 {
		return nil
	}
	cols := scan.Columns[T](true)
	table := tableName[T]()
	if bi, ok := bulkInserter(r.sess); ok {
		_, err := bi.CopyFrom(ctx, table, cols, &sliceRowSource[T]{vs: vs, cols: cols})
		return err
	}
	// Fallback: chunked multi-row VALUES (bounded to stay under bind-var limits).
	maxRows := max(900/max(1, len(cols)), 1)
	for start := 0; start < len(vs); start += maxRows {
		end := min(start+maxRows, len(vs))
		rows := make([][]any, 0, end-start)
		for i := start; i < end; i++ {
			rows = append(rows, scan.Values(&vs[i], cols))
		}
		ins := sqlgen.Insert{Table: table, Columns: cols, Rows: rows}
		q, args, err := ins.Build(r.sess.Dialect())
		if err != nil {
			return err
		}
		if _, err := r.sess.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

// bulkInserter extracts the backend's BulkInserter capability from a session
// (available on a *DB connection; transactions fall back to multi-row VALUES).
func bulkInserter(sess liteorm.Session) (liteorm.BulkInserter, bool) {
	if h, ok := sess.(interface{ Querier() liteorm.Querier }); ok {
		if bi, ok := h.Querier().(liteorm.BulkInserter); ok {
			return bi, true
		}
	}
	return nil, false
}

type sliceRowSource[T any] struct {
	vs   []T
	cols []string
	i    int
}

func (s *sliceRowSource[T]) Next() bool { s.i++; return s.i <= len(s.vs) }
func (s *sliceRowSource[T]) Values() ([]any, error) {
	return scan.Values(&s.vs[s.i-1], s.cols), nil
}
func (s *sliceRowSource[T]) Err() error { return nil }

// Upsert inserts v or, on conflict with oc.Columns, updates oc.Update (defaulting
// to all non-conflict insert columns).
func (r *Repo[T]) Upsert(ctx context.Context, v *T, oc OnConflictSpec) error {
	cols := scan.Columns[T](true)
	update := oc.Update
	if !oc.Nothing {
		if len(update) == 0 {
			update = subtract(cols, oc.Columns)
		}
		if len(update) == 0 {
			return fmt.Errorf("liteorm: upsert has no columns to update")
		}
	}
	ins := sqlgen.Insert{
		Table:      tableName[T](),
		Columns:    cols,
		Rows:       [][]any{scan.Values(v, cols)},
		OnConflict: &sqlgen.Conflict{Columns: oc.Columns, Update: update, Nothing: oc.Nothing},
	}
	return InsertCapturingPK(ctx, r.sess, ins, v)
}

// Find returns all rows matching the typed predicates.
func (r *Repo[T]) Find(ctx context.Context, preds ...Predicate) ([]T, error) {
	return Select[T](r.sess).Filter(preds...).All(ctx)
}

// InsertCapturingPK runs an INSERT and writes the generated primary key back into
// v, using the best mechanism the dialect supports: RETURNING (Postgres/SQLite)
// or OUTPUT (MSSQL) read back via a query, else LastInsertId (MySQL/SQLite). It is
// exported so the orm front-end can reuse the same capture logic.
func InsertCapturingPK[T any](ctx context.Context, sess liteorm.Session, ins sqlgen.Insert, v *T) error {
	d := sess.Dialect()
	feat := d.Features()
	pk, hasPK := scan.AutoPrimaryKey[T]()
	if hasPK && (feat.Has(dialect.FeatReturning) || feat.Has(dialect.FeatOutput)) {
		ins.Returning = []string{pk}
		q, args, err := ins.Build(d)
		if err != nil {
			return err
		}
		rows, err := sess.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			if err := scan.Into(rows, v); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	q, args, err := ins.Build(d)
	if err != nil {
		return err
	}
	res, err := sess.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if hasPK && feat.Has(dialect.FeatLastInsertID) {
		if li, ok := res.(liteorm.LastInsertIder); ok {
			if id, err := li.LastInsertId(); err == nil {
				scan.SetPrimaryKey(v, id)
			}
		}
	}
	return nil
}

// InsertManyCapturingPK runs one multi-row INSERT (ins.Rows holds the batch) and,
// on a dialect with RETURNING/OUTPUT, writes each generated primary key back into
// the matching element of vs by position. Without RETURNING it inserts the batch
// without reading keys back (callers that need generated keys on such dialects
// insert per row). It is exported so the orm front-end can reuse the capture
// logic for batched creates.
func InsertManyCapturingPK[T any](ctx context.Context, sess liteorm.Session, ins sqlgen.Insert, vs []*T) error {
	d := sess.Dialect()
	feat := d.Features()
	pk, hasPK := scan.AutoPrimaryKey[T]()
	if hasPK && (feat.Has(dialect.FeatReturning) || feat.Has(dialect.FeatOutput)) {
		ins.Returning = []string{pk}
		q, args, err := ins.Build(d)
		if err != nil {
			return err
		}
		rows, err := sess.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		i := 0
		for rows.Next() {
			if i < len(vs) {
				if err := scan.Into(rows, vs[i]); err != nil {
					return err
				}
			}
			i++
		}
		return rows.Err()
	}
	q, args, err := ins.Build(d)
	if err != nil {
		return err
	}
	_, err = sess.ExecContext(ctx, q, args...)
	return err
}

// Get fetches the row whose primary key equals the given key, or
// liteorm.ErrNoRows. For a composite key, pass one value per key column in
// declaration order: Get(ctx, tenantID, code).
func (r *Repo[T]) Get(ctx context.Context, keys ...any) (T, error) {
	var zero T
	pks := scan.PrimaryKeys[T]()
	if len(pks) == 0 {
		return zero, fmt.Errorf("liteorm: type %T has no primary key", zero)
	}
	if len(keys) != len(pks) {
		return zero, fmt.Errorf("liteorm: Get on %T needs %d primary-key value(s), got %d", zero, len(pks), len(keys))
	}
	q := Select[T](r.sess)
	d := r.sess.Dialect()
	for i, pk := range pks {
		q = q.Where(string(d.QuoteIdent(nil, pk))+" = ?", keys[i])
	}
	return q.First(ctx)
}

// pkWhereExprs builds the WHERE expressions matching v's primary key (every
// column of a composite key).
func pkWhereExprs[T any](v *T, pks []string, d dialect.Dialect) []sqlgen.Expr {
	vals := scan.Values(v, pks)
	out := make([]sqlgen.Expr, len(pks))
	for i, pk := range pks {
		out[i] = sqlgen.Expr{SQL: string(d.QuoteIdent(nil, pk)) + " = ?", Args: []any{vals[i]}}
	}
	return out
}

// Update writes v's non-key columns to the row identified by its primary key.
func (r *Repo[T]) Update(ctx context.Context, v *T) error {
	pks := scan.PrimaryKeys[T]()
	if len(pks) == 0 {
		return fmt.Errorf("liteorm: type %T has no primary key", *v)
	}
	cols := scan.Columns[T](true) // non-auto-PK columns
	vals := scan.Values(v, cols)
	set := make([]sqlgen.SetClause, len(cols))
	for i, c := range cols {
		set[i] = sqlgen.SetClause{Column: c, Arg: vals[i]}
	}
	d := r.sess.Dialect()
	up := sqlgen.Update{
		Table: tableName[T](),
		Set:   set,
		Where: pkWhereExprs(v, pks, d),
	}
	q, args, err := up.Build(d)
	if err != nil {
		return err
	}
	_, err = r.sess.ExecContext(ctx, q, args...)
	return err
}

// Delete removes the row whose primary key equals the given key (one value per
// key column for a composite key).
func (r *Repo[T]) Delete(ctx context.Context, keys ...any) error {
	var zero T
	pks := scan.PrimaryKeys[T]()
	if len(pks) == 0 {
		return fmt.Errorf("liteorm: type %T has no primary key", zero)
	}
	if len(keys) != len(pks) {
		return fmt.Errorf("liteorm: Delete on %T needs %d primary-key value(s), got %d", zero, len(pks), len(keys))
	}
	d := r.sess.Dialect()
	where := make([]sqlgen.Expr, len(pks))
	for i, pk := range pks {
		where[i] = sqlgen.Expr{SQL: string(d.QuoteIdent(nil, pk)) + " = ?", Args: []any{keys[i]}}
	}
	del := sqlgen.Delete{Table: tableName[T](), Where: where}
	q, args, err := del.Build(d)
	if err != nil {
		return err
	}
	_, err = r.sess.ExecContext(ctx, q, args...)
	return err
}

// OnConflictSpec describes an upsert conflict target and what to do on a conflict.
type OnConflictSpec struct {
	Columns []string
	Update  []string
	Nothing bool
}

// OnConflict names the conflict-target columns. Chain DoUpdate to pick the
// columns to overwrite, or DoNothing to ignore the conflicting row; with neither,
// all non-conflict columns are updated.
func OnConflict(cols ...string) OnConflictSpec { return OnConflictSpec{Columns: cols} }

// DoUpdate sets the columns to overwrite on conflict.
func (o OnConflictSpec) DoUpdate(cols ...string) OnConflictSpec {
	o.Update = cols
	return o
}

// DoNothing makes the upsert ignore a conflicting row — INSERT ... ON CONFLICT DO
// NOTHING, the typed form of INSERT OR IGNORE — instead of updating it. On MySQL
// it renders a no-op ON DUPLICATE KEY UPDATE; on SQL Server, a MERGE with no WHEN
// MATCHED arm. A skipped insert returns no generated key.
func (o OnConflictSpec) DoNothing() OnConflictSpec {
	o.Update = nil
	o.Nothing = true
	return o
}

func subtract(all, remove []string) []string {
	skip := make(map[string]bool, len(remove))
	for _, r := range remove {
		skip[r] = true
	}
	out := make([]string, 0, len(all))
	for _, a := range all {
		if !skip[a] {
			out = append(out, a)
		}
	}
	return out
}
