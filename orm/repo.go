package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
	"liteorm.org/query"
)

// DeletedScope controls how soft-deleted rows are treated by reads. The default
// (WithoutDeleted) excludes them; the scope is an explicit, nameable enum — a
// clear opt-out without a double negative.
type DeletedScope int

const (
	WithoutDeleted DeletedScope = iota
	IncludeDeleted
	OnlyDeleted
)

// Repo is the declarative repository for T over a liteorm.Session. It reuses the
// `query` front-end for reads (proving shared-core interop) and adds hooks and
// soft-delete scoping.
type Repo[T any] struct {
	sess       liteorm.Session
	scope      DeletedScope
	selectCols []string   // if set, only these columns are written
	omitCols   []string   // these columns are never written
	readScopes []Scope[T] // composed onto reads (Find/First/Count/Exists)
}

// NewRepo constructs a Repo[T] bound to sess (a *DB or a transaction).
//
// orm.Repo uses model-oriented verbs (Create, Delete(*T)) so hooks and the
// soft-delete scope can fire; this differs by design from query.Repo's
// SQL-oriented surface (Insert, Delete(id)). Reads compose a thin filter layer
// (Where/Filter/OrderBy/Limit/Offset/Scopes) over the query builder; drop to a
// full query.Select[T] on the same Session for joins, unions, and projections.
func NewRepo[T any](sess liteorm.Session) *Repo[T] { return &Repo[T]{sess: sess} }

// IncludeDeleted returns a Repo view that includes soft-deleted rows.
func (r *Repo[T]) IncludeDeleted() *Repo[T] { c := *r; c.scope = IncludeDeleted; return &c }

// OnlyDeleted returns a Repo view that returns only soft-deleted rows.
func (r *Repo[T]) OnlyDeleted() *Repo[T] { c := *r; c.scope = OnlyDeleted; return &c }

// Select returns a Repo view whose writes (Create/Update/Save) touch only the
// named columns. Tokens match a column name or a Go field name. The primary key
// and auto timestamps are still handled. Mirrors gorm's Select for writes.
func (r *Repo[T]) Select(cols ...string) *Repo[T] { c := *r; c.selectCols = cols; return &c }

// Omit returns a Repo view whose writes never touch the named columns (matched
// by column name or Go field name). Mirrors gorm's Omit.
func (r *Repo[T]) Omit(cols ...string) *Repo[T] { c := *r; c.omitCols = cols; return &c }

// writeColumns applies any Select/Omit scope on top of the schema's writable set.
func (r *Repo[T]) writeColumns(s *Schema, forUpdate bool) []string {
	cols := s.WriteColumns(forUpdate)
	if len(r.selectCols) > 0 {
		sel := r.colSet(s, r.selectCols)
		cols = filterCols(cols, func(c string) bool { return sel[c] })
	}
	if len(r.omitCols) > 0 {
		om := r.colSet(s, r.omitCols)
		cols = filterCols(cols, func(c string) bool { return !om[c] })
	}
	return cols
}

// colSet resolves Select/Omit tokens (column names or Go field names) to a set
// of column names.
func (r *Repo[T]) colSet(s *Schema, tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		for _, f := range s.Fields {
			if f.Column == tok || f.GoName == tok {
				set[f.Column] = true
				break
			}
		}
	}
	return set
}

func filterCols(cols []string, keep func(string) bool) []string {
	out := cols[:0:0]
	for _, c := range cols {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// setAutoTimes stamps time fields: an autoUpdateTime field is always set to now
// (on create and on update); an autoCreateTime field is set only on create and
// only when it is still zero, so a caller-provided value is preserved.
func setAutoTimes[T any](s *Schema, v *T, onCreate bool) {
	now := time.Now().UTC()
	rv := reflect.ValueOf(v).Elem()
	for _, f := range s.Fields {
		fv := rv.FieldByIndex(f.Index)
		switch {
		case f.AutoUpdate:
			// always stamp
		case onCreate && f.AutoCreate:
			if !isZeroTimeField(fv) {
				continue
			}
		default:
			continue
		}
		setTimeField(fv, now)
	}
}

func isZeroTimeField(fv reflect.Value) bool {
	switch x := fv.Interface().(type) {
	case time.Time:
		return x.IsZero()
	case sql.NullTime:
		return !x.Valid
	}
	return fv.Kind() == reflect.Pointer && fv.IsNil()
}

func setTimeField(fv reflect.Value, now time.Time) {
	switch fv.Interface().(type) {
	case time.Time:
		fv.Set(reflect.ValueOf(now))
	case sql.NullTime:
		fv.Set(reflect.ValueOf(sql.NullTime{Time: now, Valid: true}))
	default:
		if fv.Kind() == reflect.Pointer && fv.Type().Elem() == reflect.TypeFor[time.Time]() {
			t := now
			fv.Set(reflect.ValueOf(&t))
		}
	}
}

// qi quotes an identifier via the session's dialect.
func (r *Repo[T]) qi(col string) string { return string(r.sess.Dialect().QuoteIdent(nil, col)) }

func (r *Repo[T]) scopePredicate(s *Schema) (string, bool) {
	if s.SoftDelete == nil {
		return "", false
	}
	col := r.qi(s.SoftDelete.Column)
	switch r.scope {
	case WithoutDeleted:
		return col + " IS NULL", true
	case OnlyDeleted:
		return col + " IS NOT NULL", true
	}
	return "", false
}

// Create inserts v, firing Before/AfterCreate hooks; the generated PK is read
// back via RETURNING where supported.
func (r *Repo[T]) Create(ctx context.Context, v *T) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	cols := r.writeColumns(s, false)
	ev := &Event[T]{Sess: r.sess, Model: v, Columns: cols}
	if err := fireBeforeCreate(ctx, ev); err != nil {
		return err
	}
	setAutoTimes(s, v, true)
	row, err := scan.EncodeValues(v, cols)
	if err != nil {
		return err
	}
	ins := sqlgen.Insert{Table: s.Table, Columns: cols, Rows: [][]any{row}}
	if err := query.InsertCapturingPK(ctx, r.sess, ins, v); err != nil {
		return err
	}
	return fireAfterCreate(ctx, ev)
}

// Get fetches the row whose primary key equals the given key (honoring the
// soft-delete scope), or liteorm.ErrNoRows. For a composite primary key, pass one
// value per key column, in declaration order: Get(ctx, tenantID, code).
func (r *Repo[T]) Get(ctx context.Context, keys ...any) (T, error) {
	var zero T
	s, err := SchemaOf[T]()
	if err != nil {
		return zero, err
	}
	if len(s.PKs) == 0 {
		return zero, fmt.Errorf("orm: type %T has no primary key", zero)
	}
	if len(keys) != len(s.PKs) {
		return zero, fmt.Errorf("orm: Get on %T needs %d primary-key value(s), got %d", zero, len(s.PKs), len(keys))
	}
	q := query.Select[T](r.sess)
	for i, pk := range s.PKs {
		q = q.Where(r.qi(pk.Column)+" = ?", keys[i])
	}
	if pred, ok := r.scopePredicate(s); ok {
		q = q.Where(pred)
	}
	out, err := q.Limit(1).All(ctx)
	if err != nil {
		return zero, err
	}
	if len(out) == 0 {
		return zero, liteorm.ErrNoRows
	}
	if err := fireAfterFind(ctx, r.sess, out); err != nil {
		return zero, err
	}
	return out[0], nil
}

// GetByKeys fetches the rows whose primary key is one of keys, in a single query
// (honoring the soft-delete scope) — the batch form of Get. It requires a
// single-column primary key (a clear error on a composite key). Rows come back in
// no particular order, and a key with no row is simply absent.
func (r *Repo[T]) GetByKeys(ctx context.Context, keys ...any) ([]T, error) {
	var zero T
	s, err := SchemaOf[T]()
	if err != nil {
		return nil, err
	}
	if len(s.PKs) != 1 {
		return nil, fmt.Errorf("orm: GetByKeys requires a single-column primary key on %T", zero)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	q := query.Select[T](r.sess).Where(inClause(r.sess.Dialect(), s.PK.Column, len(keys)), keys...)
	if pred, ok := r.scopePredicate(s); ok {
		q = q.Where(pred)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	if err := fireAfterFind(ctx, r.sess, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// pkWhere builds the WHERE expressions matching v's primary key — every column of
// a composite key. The caller has already checked len(s.PKs) > 0.
func (r *Repo[T]) pkWhere(s *Schema, v *T) []sqlgen.Expr {
	cols := make([]string, len(s.PKs))
	for i, pk := range s.PKs {
		cols[i] = pk.Column
	}
	vals := scan.Values(v, cols)
	out := make([]sqlgen.Expr, len(s.PKs))
	for i, pk := range s.PKs {
		out[i] = sqlgen.Expr{SQL: r.qi(pk.Column) + " = ?", Args: []any{vals[i]}}
	}
	return out
}

// Find returns all rows matching the soft-delete scope and any read scopes
// composed via Where/Filter/OrderBy/Limit/Offset/Scopes.
func (r *Repo[T]) Find(ctx context.Context) ([]T, error) {
	q, err := r.selectBuilder()
	if err != nil {
		return nil, err
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	if err := fireAfterFind(ctx, r.sess, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Update writes v's non-key columns to the row identified by its PK, firing
// Before/AfterUpdate hooks.
func (r *Repo[T]) Update(ctx context.Context, v *T) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	if len(s.PKs) == 0 {
		return fmt.Errorf("orm: type %T has no primary key", *v)
	}
	cols := r.writeColumns(s, true)
	ev := &Event[T]{Sess: r.sess, Model: v, Columns: cols}
	if err := fireBeforeUpdate(ctx, ev); err != nil {
		return err
	}
	setAutoTimes(s, v, false)
	vals, err := scan.EncodeValues(v, cols)
	if err != nil {
		return err
	}
	set := make([]sqlgen.SetClause, len(cols))
	for i, c := range cols {
		set[i] = sqlgen.SetClause{Column: c, Arg: vals[i]}
	}
	where := r.pkWhere(s, v)
	if pred, ok := r.scopePredicate(s); ok { // don't update an out-of-scope (e.g. soft-deleted) row
		where = append(where, sqlgen.Expr{SQL: pred})
	}
	up := sqlgen.Update{Table: s.Table, Set: set, Where: where}
	q, args, err := up.Build(r.sess.Dialect())
	if err != nil {
		return err
	}
	res, err := r.sess.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	// A keyed update that matched no row (a wrong PK, or a soft-deleted row that is
	// out of the current scope) is ErrNoRows, not a silent no-ev.
	if res.RowsAffected() == 0 {
		return liteorm.ErrNoRows
	}
	return fireAfterUpdate(ctx, ev)
}

// Delete removes v: a soft delete (UPDATE deleted_at = now) when the model has a
// soft-delete column, else a hard DELETE. Always scoped by PK (no WHERE-less
// delete). Fires Before/AfterDelete hooks.
func (r *Repo[T]) Delete(ctx context.Context, v *T) error {
	return r.delete(ctx, v, false)
}

// ForceDelete always issues a hard DELETE, even for soft-delete models.
func (r *Repo[T]) ForceDelete(ctx context.Context, v *T) error {
	return r.delete(ctx, v, true)
}

func (r *Repo[T]) delete(ctx context.Context, v *T, force bool) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	if len(s.PKs) == 0 {
		return fmt.Errorf("orm: type %T has no primary key", *v)
	}
	ev := &Event[T]{Sess: r.sess, Model: v}
	if err := fireBeforeDelete(ctx, ev); err != nil {
		return err
	}
	where := r.pkWhere(s, v)
	if !force { // a force delete purges regardless of scope; a normal delete respects it
		if pred, ok := r.scopePredicate(s); ok {
			where = append(where, sqlgen.Expr{SQL: pred})
		}
	}
	d := r.sess.Dialect()

	var stmt string
	var args []any
	if s.SoftDelete != nil && !force {
		// A soft delete is an UPDATE, so it also bumps any autoUpdateTime column.
		setAutoTimes(s, v, false)
		set := []sqlgen.SetClause{{Column: s.SoftDelete.Column, Arg: time.Now().UTC()}}
		for _, f := range s.Fields {
			if f.AutoUpdate {
				av, encErr := scan.EncodeValues(v, []string{f.Column})
				if encErr != nil {
					return encErr
				}
				set = append(set, sqlgen.SetClause{Column: f.Column, Arg: av[0]})
			}
		}
		up := sqlgen.Update{Table: s.Table, Set: set, Where: where}
		stmt, args, err = up.Build(d)
	} else {
		del := sqlgen.Delete{Table: s.Table, Where: where}
		stmt, args, err = del.Build(d)
	}
	if err != nil {
		return err
	}
	res, err := r.sess.ExecContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	// A keyed delete that matched no row (a wrong PK, or an already-deleted row out
	// of the current scope) is ErrNoRows, not a silent no-ev.
	if res.RowsAffected() == 0 {
		return liteorm.ErrNoRows
	}
	if force || s.SoftDelete == nil { // a hard delete removes the row from hook-synced sidecars too
		if err := syncSearchDelete(ctx, r.sess, s, v); err != nil {
			return err
		}
	}
	return fireAfterDelete(ctx, ev)
}
