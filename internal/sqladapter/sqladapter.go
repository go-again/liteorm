// Package sqladapter adapts a database/sql *sql.DB to liteorm's core contracts,
// shared by every database/sql-based backend (SQLite, MySQL, MSSQL). Each backend
// supplies its dialect and an error-normalization function; this package provides
// the Querier/Rows/Result/Beginner/Tx adapters and savepoint-based nested
// transactions. (The native-pgx Postgres backend does NOT use this — it wraps
// pgx directly.)
package sqladapter

import (
	"context"
	"database/sql"
	"fmt"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
)

// NormalizeFunc maps a driver error to a liteorm sentinel (or returns it as-is).
type NormalizeFunc func(error) error

// Open wraps db as a liteorm.DB on dialect d, normalizing driver errors via norm.
func Open(db *sql.DB, d dialect.Dialect, norm NormalizeFunc, opts ...liteorm.Option) *liteorm.DB {
	return liteorm.New(&conn{db: db, d: d, norm: norm}, d, opts...)
}

// TxBeginner is implemented by both *sql.DB and a pinned *sql.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// WrapRows adapts a *sql.Rows to liteorm.Rows, normalizing Err via norm. Backends
// that wrap a driver other than plain *sql.DB (e.g. the gosqlite-backed SQLite
// adapter) reuse this instead of reimplementing the row adapter.
func WrapRows(rows *sql.Rows, norm NormalizeFunc) liteorm.Rows {
	if norm == nil {
		norm = func(e error) error { return e }
	}
	return &rowsAdapter{rows: rows, norm: norm}
}

// WrapResult adapts a sql.Result to liteorm.Result (also a LastInsertIder).
func WrapResult(res sql.Result) liteorm.Result { return resultAdapter{res: res} }

// BeginTx starts a transaction on src (a *sql.DB or a pinned *sql.Conn) and
// adapts it to liteorm.Tx with savepoint-based nested transactions.
func BeginTx(ctx context.Context, src TxBeginner, d dialect.Dialect, norm NormalizeFunc, opts liteorm.TxOptions) (liteorm.Tx, error) {
	if norm == nil {
		norm = func(e error) error { return e }
	}
	tx, err := src.BeginTx(ctx, txOpts(opts))
	if err != nil {
		return nil, norm(err)
	}
	return &txAdapter{txQuerier: txQuerier{tx: tx, d: d, norm: norm}}, nil
}

type conn struct {
	db   *sql.DB
	d    dialect.Dialect
	norm NormalizeFunc
}

func (c *conn) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, c.norm(err)
	}
	return &rowsAdapter{rows: rows, norm: c.norm}, nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, c.norm(err)
	}
	return resultAdapter{res: res}, nil
}

func (c *conn) Begin(ctx context.Context) (liteorm.Tx, error) {
	return c.BeginTx(ctx, liteorm.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts liteorm.TxOptions) (liteorm.Tx, error) {
	tx, err := c.db.BeginTx(ctx, txOpts(opts))
	if err != nil {
		return nil, c.norm(err)
	}
	return &txAdapter{txQuerier: txQuerier{tx: tx, d: c.d, norm: c.norm}}, nil
}

func (c *conn) Close() error { return c.db.Close() }

// Stats reports the database/sql connection-pool statistics, satisfying
// liteorm.StatsProvider (so every sql-backed dialect — MySQL, SQL Server — exposes
// pool health to schema browsers and admin tooling).
func (c *conn) Stats() liteorm.PoolStats {
	s := c.db.Stats()
	return liteorm.PoolStats{
		MaxOpenConnections: s.MaxOpenConnections,
		OpenConnections:    s.OpenConnections,
		InUse:              s.InUse,
		Idle:               s.Idle,
		WaitCount:          s.WaitCount,
		WaitDuration:       s.WaitDuration,
		MaxIdleClosed:      s.MaxIdleClosed,
		MaxIdleTimeClosed:  s.MaxIdleTimeClosed,
		MaxLifetimeClosed:  s.MaxLifetimeClosed,
	}
}

func txOpts(o liteorm.TxOptions) *sql.TxOptions {
	lvl := sql.LevelDefault
	switch o.IsoLevel {
	case "serializable":
		lvl = sql.LevelSerializable
	case "repeatable read":
		lvl = sql.LevelRepeatableRead
	case "read committed":
		lvl = sql.LevelReadCommitted
	case "read uncommitted":
		lvl = sql.LevelReadUncommitted
	}
	return &sql.TxOptions{Isolation: lvl, ReadOnly: o.ReadOnly}
}

type rowsAdapter struct {
	rows *sql.Rows
	norm NormalizeFunc
}

func (r *rowsAdapter) Next() bool                 { return r.rows.Next() }
func (r *rowsAdapter) Scan(dest ...any) error     { return r.rows.Scan(dest...) }
func (r *rowsAdapter) Columns() ([]string, error) { return r.rows.Columns() }
func (r *rowsAdapter) Close() error               { return r.rows.Close() }
func (r *rowsAdapter) Err() error                 { return r.norm(r.rows.Err()) }

func (r *rowsAdapter) Values() ([]any, error) {
	cols, err := r.rows.Columns()
	if err != nil {
		return nil, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return vals, nil
}

type resultAdapter struct{ res sql.Result }

func (r resultAdapter) RowsAffected() int64 {
	n, _ := r.res.RowsAffected()
	return n
}

// LastInsertId is exposed unconditionally, so resultAdapter advertises
// liteorm.LastInsertIder for every database/sql backend — including MSSQL, whose
// driver returns no usable id. Callers must gate on the dialect's
// FeatLastInsertID before using it (see query.Repo's insert path); the
// capability assertion alone is not a promise the value is meaningful.
func (r resultAdapter) LastInsertId() (int64, error) { return r.res.LastInsertId() }

type txQuerier struct {
	tx   *sql.Tx
	d    dialect.Dialect
	norm NormalizeFunc
}

func (t txQuerier) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, t.norm(err)
	}
	return &rowsAdapter{rows: rows, norm: t.norm}, nil
}

func (t txQuerier) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	res, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, t.norm(err)
	}
	return resultAdapter{res: res}, nil
}

type txAdapter struct {
	txQuerier
	depth int
}

func (t *txAdapter) Begin(ctx context.Context) (liteorm.Tx, error) {
	return beginSavepoint(ctx, t.txQuerier, t.depth)
}
func (t *txAdapter) Commit(context.Context) error   { return t.norm(t.tx.Commit()) }
func (t *txAdapter) Rollback(context.Context) error { return t.norm(t.tx.Rollback()) }

type savepoint struct {
	txQuerier
	depth int
	name  string
}

func (s *savepoint) Begin(ctx context.Context) (liteorm.Tx, error) {
	return beginSavepoint(ctx, s.txQuerier, s.depth)
}
func (s *savepoint) Commit(ctx context.Context) error {
	// Releasing a savepoint keeps its changes pending in the outer tx. T-SQL has
	// no RELEASE — the SAVE TRANSACTION marker just persists — so it's a no-op there.
	if rel := releaseSavepointSQL(s.d, s.name); rel != "" {
		if _, err := s.tx.ExecContext(ctx, rel); err != nil {
			return s.norm(err)
		}
	}
	return nil
}
func (s *savepoint) Rollback(ctx context.Context) error {
	if _, err := s.tx.ExecContext(ctx, rollbackToSavepointSQL(s.d, s.name)); err != nil {
		return s.norm(err)
	}
	if rel := releaseSavepointSQL(s.d, s.name); rel != "" {
		if _, err := s.tx.ExecContext(ctx, rel); err != nil {
			return s.norm(err)
		}
	}
	return nil
}

func beginSavepoint(ctx context.Context, q txQuerier, depth int) (liteorm.Tx, error) {
	name := fmt.Sprintf("liteorm_sp_%d", depth+1)
	if _, err := q.tx.ExecContext(ctx, saveSavepointSQL(q.d, name)); err != nil {
		return nil, q.norm(err)
	}
	return &savepoint{txQuerier: q, depth: depth + 1, name: name}, nil
}

// Savepoint syntax: ANSI (SQLite/MySQL/Postgres) vs T-SQL (MSSQL), which uses
// SAVE/ROLLBACK TRANSACTION and has no RELEASE.
func saveSavepointSQL(d dialect.Dialect, name string) string {
	if d.Name() == "mssql" {
		return "SAVE TRANSACTION " + name
	}
	return "SAVEPOINT " + name
}
func rollbackToSavepointSQL(d dialect.Dialect, name string) string {
	if d.Name() == "mssql" {
		return "ROLLBACK TRANSACTION " + name
	}
	return "ROLLBACK TO SAVEPOINT " + name
}
func releaseSavepointSQL(d dialect.Dialect, name string) string {
	if d.Name() == "mssql" {
		return ""
	}
	return "RELEASE SAVEPOINT " + name
}

// Compile-time checks.
var (
	_ liteorm.Querier        = (*conn)(nil)
	_ liteorm.Beginner       = (*conn)(nil)
	_ liteorm.Tx             = (*txAdapter)(nil)
	_ liteorm.Tx             = (*savepoint)(nil)
	_ liteorm.Rows           = (*rowsAdapter)(nil)
	_ liteorm.Result         = resultAdapter{}
	_ liteorm.LastInsertIder = resultAdapter{}
)
