// Package postgres is liteorm's Postgres backend over the NATIVE pgx/v5 API
// (pgxpool, not the database/sql shim), giving the binary protocol scan path and
// the native fast-paths (CopyFrom bulk, savepoints). It adapts pgx to liteorm's
// core contracts while keeping pgx types off the public surface: pgconn.CommandTag
// becomes liteorm.Result, pgconn.PgError is normalized to liteorm sentinels, and
// Scan (not Values) is the primary, leak-free read path.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	liteorm "liteorm.org"
	"liteorm.org/internal/sqlgen"
)

// Open creates a pgx connection pool for dsn and returns a liteorm.DB on the
// Postgres dialect.
func Open(ctx context.Context, dsn string, opts ...liteorm.Option) (*liteorm.DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return liteorm.New(&conn{pool: pool}, sqlgen.Postgres, opts...), nil
}

// conn adapts a native *pgxpool.Pool to liteorm's contracts and capabilities.
type conn struct {
	pool *pgxpool.Pool
}

func (c *conn) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := c.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &rowsAdapter{rows: rows}, nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	ct, err := c.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return result{ct: ct}, nil
}

func (c *conn) Begin(ctx context.Context) (liteorm.Tx, error) {
	return c.BeginTx(ctx, liteorm.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts liteorm.TxOptions) (liteorm.Tx, error) {
	tx, err := c.pool.BeginTx(ctx, pgTxOpts(opts))
	if err != nil {
		return nil, normalizeError(err)
	}
	return &txAdapter{tx: tx}, nil
}

// Close closes the pool.
func (c *conn) Close() error { c.pool.Close(); return nil }

// Stats maps the pgxpool statistics onto liteorm.PoolStats (satisfying
// liteorm.StatsProvider), so Postgres exposes pool health like the sql-backed
// dialects. pgx tracks acquire waits rather than per-reason idle closes, so
// MaxIdleTimeClosed is left zero.
func (c *conn) Stats() liteorm.PoolStats {
	s := c.pool.Stat()
	return liteorm.PoolStats{
		MaxOpenConnections: int(s.MaxConns()),
		OpenConnections:    int(s.TotalConns()),
		InUse:              int(s.AcquiredConns()),
		Idle:               int(s.IdleConns()),
		WaitCount:          s.EmptyAcquireCount(),
		WaitDuration:       s.EmptyAcquireWaitTime(),
		MaxIdleClosed:      s.MaxIdleDestroyCount(),
		MaxLifetimeClosed:  s.MaxLifetimeDestroyCount(),
	}
}

// CopyFrom is the native bulk-insert fast path (Postgres COPY). The liteorm
// RowSource is shimmed to pgx.CopyFromSource so the driver type never appears in
// liteorm's public API. table is treated as a single unqualified identifier; a
// schema-qualified target is not supported on this path.
func (c *conn) CopyFrom(ctx context.Context, table string, columns []string, src liteorm.RowSource) (int64, error) {
	n, err := c.pool.CopyFrom(ctx, pgx.Identifier{table}, columns, &copySource{src: src})
	return n, normalizeError(err)
}

func pgTxOpts(o liteorm.TxOptions) pgx.TxOptions {
	var opt pgx.TxOptions
	switch o.IsoLevel {
	case "serializable":
		opt.IsoLevel = pgx.Serializable
	case "repeatable read":
		opt.IsoLevel = pgx.RepeatableRead
	case "read committed":
		opt.IsoLevel = pgx.ReadCommitted
	case "read uncommitted":
		opt.IsoLevel = pgx.ReadUncommitted
	}
	if o.ReadOnly {
		opt.AccessMode = pgx.ReadOnly
	}
	return opt
}

// rowsAdapter wraps pgx.Rows as liteorm.Rows. CommandTag/FieldDescriptions/Conn
// are deliberately NOT exposed; Columns maps field-description names.
type rowsAdapter struct{ rows pgx.Rows }

func (r *rowsAdapter) Next() bool             { return r.rows.Next() }
func (r *rowsAdapter) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *rowsAdapter) Columns() ([]string, error) {
	fds := r.rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, fd := range fds {
		cols[i] = fd.Name
	}
	return cols, nil
}
func (r *rowsAdapter) Close() error { r.rows.Close(); return nil }
func (r *rowsAdapter) Err() error   { return normalizeError(r.rows.Err()) }

// Values is advanced/best-effort: on pgx-native it may surface pgtype values for
// exotic columns (the one intrinsic leak documented in R3). Prefer Scan.
func (r *rowsAdapter) Values() ([]any, error) { return r.rows.Values() }

// result wraps pgconn.CommandTag as liteorm.Result. It intentionally does NOT
// implement LastInsertIder — Postgres uses RETURNING instead.
type result struct{ ct pgconn.CommandTag }

func (r result) RowsAffected() int64 { return r.ct.RowsAffected() }

// txAdapter wraps pgx.Tx. Nested Begin uses pgx's native savepoint machinery,
// giving the uniform nested-Begin model the core expects.
type txAdapter struct{ tx pgx.Tx }

func (t *txAdapter) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &rowsAdapter{rows: rows}, nil
}
func (t *txAdapter) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	ct, err := t.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return result{ct: ct}, nil
}
func (t *txAdapter) Begin(ctx context.Context) (liteorm.Tx, error) {
	inner, err := t.tx.Begin(ctx)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &txAdapter{tx: inner}, nil
}
func (t *txAdapter) Commit(ctx context.Context) error   { return normalizeError(t.tx.Commit(ctx)) }
func (t *txAdapter) Rollback(ctx context.Context) error { return normalizeError(t.tx.Rollback(ctx)) }

// copySource shims a liteorm.RowSource to pgx.CopyFromSource (method-identical).
type copySource struct{ src liteorm.RowSource }

func (c *copySource) Next() bool             { return c.src.Next() }
func (c *copySource) Values() ([]any, error) { return c.src.Values() }
func (c *copySource) Err() error             { return c.src.Err() }

// normalizeError maps pgconn.PgError SQLSTATEs to liteorm sentinels, dual-wrapping
// so both errors.Is (sentinel) and errors.As (driver error) stay reachable. pgx
// already proxies sql.ErrNoRows, so liteorm.ErrNoRows works unchanged.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return fmt.Errorf("%w: %w", liteorm.ErrUniqueViolation, err)
		case "23503":
			return fmt.Errorf("%w: %w", liteorm.ErrForeignKey, err)
		case "23502":
			return fmt.Errorf("%w: %w", liteorm.ErrNotNull, err)
		case "23514":
			return fmt.Errorf("%w: %w", liteorm.ErrCheck, err)
		case "40P01":
			return fmt.Errorf("%w: %w", liteorm.ErrDeadlock, err)
		case "40001":
			return fmt.Errorf("%w: %w", liteorm.ErrSerialization, err)
		}
	}
	return err
}

// Compile-time checks against the core contracts and capabilities.
var (
	_ liteorm.Querier      = (*conn)(nil)
	_ liteorm.Beginner     = (*conn)(nil)
	_ liteorm.BulkInserter = (*conn)(nil)
	_ liteorm.Tx           = (*txAdapter)(nil)
	_ liteorm.Rows         = (*rowsAdapter)(nil)
	_ liteorm.Result       = result{}
)
