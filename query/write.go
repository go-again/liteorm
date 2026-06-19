package query

import (
	"context"
	"fmt"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
)

// UpdateBuilder builds a multi-row UPDATE over T's table — the WHERE-based
// counterpart to Repo.Update (which updates one row by primary key). Set the
// columns, scope it with Where/Filter, optionally correlate with another source
// via From, then Exec (rows affected) or Returning (the changed rows as []T).
type UpdateBuilder[T any] struct {
	sess         liteorm.Session
	up           sqlgen.Update
	preds        []Predicate
	requiredFeat dialect.Feature
	buildErr     error
}

// Update starts an UPDATE over T's table.
func Update[T any](sess liteorm.Session) *UpdateBuilder[T] {
	return &UpdateBuilder[T]{sess: sess, up: sqlgen.Update{Table: tableName[T]()}}
}

// Set assigns a column to a bound value.
func (u *UpdateBuilder[T]) Set(col string, val any) *UpdateBuilder[T] {
	u.up.Set = append(u.up.Set, sqlgen.SetClause{Column: col, Arg: val})
	return u
}

// SetExpr assigns a column to a raw SQL expression (which may carry "?" markers) —
// e.g. SetExpr("views", "views + 1") or, with From, SetExpr("price", "src.price").
func (u *UpdateBuilder[T]) SetExpr(col, sqlExpr string, args ...any) *UpdateBuilder[T] {
	u.up.Set = append(u.up.Set, sqlgen.SetClause{Column: col, SQL: sqlExpr, Args: args})
	return u
}

// Inc atomically increments col by `by` in the UPDATE (col = col + by) — the typed
// form of SetExpr("col", "col + 1"), with the column quoted for the dialect so the
// read-modify-write happens in the database. Dec decrements. The column is still
// validated against T at build time.
func (u *UpdateBuilder[T]) Inc(col string, by int64) *UpdateBuilder[T] {
	return u.SetExpr(col, quoteCol(u.sess.Dialect(), col)+" + ?", by)
}

// Dec atomically decrements col by `by` (col = col - by).
func (u *UpdateBuilder[T]) Dec(col string, by int64) *UpdateBuilder[T] {
	return u.SetExpr(col, quoteCol(u.sess.Dialect(), col)+" - ?", by)
}

// Where adds a raw AND-joined predicate fragment ("?" markers bound by args).
func (u *UpdateBuilder[T]) Where(frag string, args ...any) *UpdateBuilder[T] {
	u.up.Where = append(u.up.Where, sqlgen.Expr{SQL: frag, Args: args})
	return u
}

// Filter adds typed, column-validated predicates (AND-joined).
func (u *UpdateBuilder[T]) Filter(preds ...Predicate) *UpdateBuilder[T] {
	u.preds = append(u.preds, preds...)
	return u
}

// From adds a correlated `FROM <source>` (UPDATE … FROM), so SetExpr and the
// WHERE may reference the source's columns. The source is raw SQL (a table, a
// derived table, or a VALUES list) and may carry "?" markers. Gated by
// FeatUpdateFrom (Postgres / SQLite / SQL Server; MySQL uses UPDATE … JOIN and
// raises a clear build error).
func (u *UpdateBuilder[T]) From(source string, args ...any) *UpdateBuilder[T] {
	u.up.From = &sqlgen.Expr{SQL: source, Args: args}
	u.requiredFeat |= dialect.FeatUpdateFrom
	return u
}

func (u *UpdateBuilder[T]) resolved() (sqlgen.Update, error) {
	up := u.up
	if u.buildErr != nil {
		return up, u.buildErr
	}
	d := u.sess.Dialect()
	if err := gateFeatures(u.requiredFeat, d); err != nil {
		return up, err
	}
	cols := columnSet[T]()
	for _, s := range u.up.Set {
		if err := unknownCol[T](cols, s.Column); err != nil {
			return up, err
		}
	}
	where, err := renderPreds[T](u.up.Where, u.preds, cols, d)
	if err != nil {
		return up, err
	}
	up.Where = where
	if err := requireWhere[T]("UPDATE", up.Where); err != nil {
		return up, err
	}
	return up, nil
}

// Exec runs the UPDATE and returns the number of rows affected.
func (u *UpdateBuilder[T]) Exec(ctx context.Context) (int64, error) {
	up, err := u.resolved()
	if err != nil {
		return 0, err
	}
	q, args, err := up.Build(u.sess.Dialect())
	if err != nil {
		return 0, err
	}
	return execAffected(ctx, u.sess, q, args)
}

// Returning runs the UPDATE and returns the changed rows as []T via RETURNING
// (Postgres/SQLite) or OUTPUT (SQL Server). It errors on a dialect with neither
// (MySQL).
func (u *UpdateBuilder[T]) Returning(ctx context.Context) ([]T, error) {
	d := u.sess.Dialect()
	if err := checkReturning(d); err != nil {
		return nil, err
	}
	up, err := u.resolved()
	if err != nil {
		return nil, err
	}
	up.Returning = scan.Columns[T](false)
	q, args, err := up.Build(d)
	if err != nil {
		return nil, err
	}
	return Raw[T](ctx, u.sess, q, args...)
}

// DeleteBuilder builds a multi-row DELETE over T's table — the WHERE-based
// counterpart to Repo.Delete (which deletes one row by primary key). Correlated
// deletes are expressed via Where/Filter with a subquery predicate (InQuery /
// Exists). Returning yields the deleted rows.
type DeleteBuilder[T any] struct {
	sess     liteorm.Session
	del      sqlgen.Delete
	preds    []Predicate
	buildErr error
}

// Delete starts a DELETE over T's table.
func Delete[T any](sess liteorm.Session) *DeleteBuilder[T] {
	return &DeleteBuilder[T]{sess: sess, del: sqlgen.Delete{Table: tableName[T]()}}
}

// Where adds a raw AND-joined predicate fragment ("?" markers bound by args).
func (b *DeleteBuilder[T]) Where(frag string, args ...any) *DeleteBuilder[T] {
	b.del.Where = append(b.del.Where, sqlgen.Expr{SQL: frag, Args: args})
	return b
}

// Filter adds typed, column-validated predicates (AND-joined).
func (b *DeleteBuilder[T]) Filter(preds ...Predicate) *DeleteBuilder[T] {
	b.preds = append(b.preds, preds...)
	return b
}

func (b *DeleteBuilder[T]) resolved() (sqlgen.Delete, error) {
	del := b.del
	if b.buildErr != nil {
		return del, b.buildErr
	}
	d := b.sess.Dialect()
	where, err := renderPreds[T](b.del.Where, b.preds, columnSet[T](), d)
	if err != nil {
		return del, err
	}
	del.Where = where
	if err := requireWhere[T]("DELETE", del.Where); err != nil {
		return del, err
	}
	return del, nil
}

// Exec runs the DELETE and returns the number of rows affected.
func (b *DeleteBuilder[T]) Exec(ctx context.Context) (int64, error) {
	del, err := b.resolved()
	if err != nil {
		return 0, err
	}
	q, args, err := del.Build(b.sess.Dialect())
	if err != nil {
		return 0, err
	}
	return execAffected(ctx, b.sess, q, args)
}

// Returning runs the DELETE and returns the deleted rows as []T via RETURNING
// (Postgres/SQLite) or OUTPUT (SQL Server). It errors on a dialect with neither.
func (b *DeleteBuilder[T]) Returning(ctx context.Context) ([]T, error) {
	d := b.sess.Dialect()
	if err := checkReturning(d); err != nil {
		return nil, err
	}
	del, err := b.resolved()
	if err != nil {
		return nil, err
	}
	del.Returning = scan.Columns[T](false)
	q, args, err := del.Build(d)
	if err != nil {
		return nil, err
	}
	return Raw[T](ctx, b.sess, q, args...)
}

// requireWhere refuses a WHERE-less UPDATE/DELETE — a guard against an accidental
// whole-table write. An explicit Where("1 = 1") opts in.
func requireWhere[T any](verb string, where []sqlgen.Expr) error {
	if len(where) == 0 {
		return fmt.Errorf("query: refusing a WHERE-less %s on %q — add a Where/Filter (use Where(\"1 = 1\") to affect every row on purpose)", verb, tableName[T]())
	}
	return nil
}

// checkReturning rejects a RETURNING request on a dialect that has neither
// RETURNING nor OUTPUT (MySQL).
func checkReturning(d dialect.Dialect) error {
	if !d.Features().Has(dialect.FeatReturning) && !d.Features().Has(dialect.FeatOutput) {
		return fmt.Errorf("query: RETURNING is not supported by the %s dialect", d.Name())
	}
	return nil
}

// execAffected runs a write statement and returns the rows affected.
func execAffected(ctx context.Context, sess liteorm.Session, q string, args []any) (int64, error) {
	res, err := sess.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}
