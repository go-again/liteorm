// Package sqlite is liteorm's SQLite backend. It wraps the sibling pure-Go
// driver gosqlite.org (it does NOT reimplement SQLite) and adapts its
// database/sql surface to liteorm's core contracts: Querier, Rows, Result,
// Beginner, Tx (with savepoint-nested Begin), the LastInsertIder capability, and
// SQLite error normalization via the driver's extended result codes.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	gosqlite "gosqlite.org"
	liteorm "liteorm.org"
	"liteorm.org/internal/sqladapter"
	"liteorm.org/internal/sqlgen"
)

// Open opens a SQLite database at path via gosqlite.org with the production
// pragma preset (WAL + busy_timeout=5s + foreign_keys=on) and returns a
// liteorm.DB on the SQLite dialect.
func Open(path string, opts ...liteorm.Option) (*liteorm.DB, error) {
	return OpenConfig(gosqlite.Config{
		Path:    path,
		Pragmas: gosqlite.RecommendedPragmas(),
	}, opts...)
}

// OpenConfig opens SQLite from a full gosqlite.Config, for callers that need
// encryption, a custom VFS, or non-default pragmas/pool sizing. The returned
// liteorm.DB carries the SQLite dialect; pass the same config's advanced fields
// (e.g. Encryption) straight through to the driver.
func OpenConfig(cfg gosqlite.Config, opts ...liteorm.Option) (*liteorm.DB, error) {
	g, err := gosqlite.Open(cfg)
	if err != nil {
		return nil, err
	}
	return liteorm.New(&conn{gdb: g}, sqlgen.SQLite, opts...), nil
}

// OpenEncrypted opens an at-rest-encrypted SQLite database at path using
// gosqlite's default Adiantum cipher (a 32-byte key). Encryption refuses
// ":memory:" and is mutually exclusive with a custom VFS — see gosqlite's
// Config.Encryption. The recommended pragma preset still applies.
func OpenEncrypted(path string, key []byte, opts ...liteorm.Option) (*liteorm.DB, error) {
	return OpenConfig(gosqlite.Config{
		Path:       path,
		Pragmas:    gosqlite.RecommendedPragmas(),
		Encryption: &gosqlite.Encryption{Key: key, Cipher: gosqlite.Adiantum},
	}, opts...)
}

// Conn returns the underlying *gosqlite.DB for a liteorm.Session opened by this
// package, giving the SQLite-specific subpackages (search, changeset) access to
// gosqlite's typed surface (vec, FTS5, sessions) and the encryption recorder.
// The second result is false for any other backend or for a transaction handle
// (those features operate at the database level). It never leaks a gosqlite type
// into the core: this lives in the SQLite backend, which already depends on it.
func Conn(sess liteorm.Session) (*gosqlite.DB, bool) {
	qp, ok := sess.(interface{ Querier() liteorm.Querier })
	if !ok {
		return nil, false
	}
	c, ok := qp.Querier().(*conn)
	if !ok {
		return nil, false
	}
	return c.gdb, true
}

// Pin acquires a single dedicated connection from sess's pool and returns it
// both as a liteorm.Session (so liteorm repos and raw exec run on exactly this
// physical connection) and as the gosqlite *Conn — the receiver for connection-
// scoped features such as the SESSION/changeset extension. Call release when
// done to return the connection to the pool; until then the *Conn stays valid.
// sess must be a *liteorm.DB opened by this package (not a transaction).
func Pin(ctx context.Context, sess liteorm.Session) (bound *liteorm.DB, gc *gosqlite.Conn, release func() error, err error) {
	g, ok := Conn(sess)
	if !ok {
		return nil, nil, nil, errors.New("liteorm/sqlite: Pin requires a *liteorm.DB opened by dialect/sqlite")
	}
	sc, err := g.Conn(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := sc.Raw(func(driverConn any) error {
		c, ok := driverConn.(*gosqlite.Conn)
		if !ok {
			return fmt.Errorf("liteorm/sqlite: unexpected driver conn %T", driverConn)
		}
		gc = c
		return nil
	}); err != nil {
		_ = sc.Close()
		return nil, nil, nil, err
	}
	return liteorm.New(&pinnedConn{sc: sc}, sqlgen.SQLite), gc, sc.Close, nil
}

// conn adapts a *gosqlite.DB (which embeds *sql.DB) to liteorm.Querier +
// liteorm.Beginner + io.Closer.
type conn struct {
	gdb *gosqlite.DB
}

// pinnedConn adapts a single pooled *sql.Conn to the core contracts via the
// shared sqladapter helpers. It deliberately has no Close: the pinned connection
// is released via the func Pin returns, not by closing the session.
type pinnedConn struct{ sc *sql.Conn }

func (p *pinnedConn) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := p.sc.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return sqladapter.WrapRows(rows, normalizeError), nil
}

func (p *pinnedConn) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	res, err := p.sc.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return sqladapter.WrapResult(res), nil
}

func (p *pinnedConn) Begin(ctx context.Context) (liteorm.Tx, error) {
	return p.BeginTx(ctx, liteorm.TxOptions{})
}

func (p *pinnedConn) BeginTx(ctx context.Context, opts liteorm.TxOptions) (liteorm.Tx, error) {
	return sqladapter.BeginTx(ctx, p.sc, sqlgen.SQLite, normalizeError, opts)
}

func (c *conn) QueryContext(ctx context.Context, query string, args ...any) (liteorm.Rows, error) {
	rows, err := c.gdb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return sqladapter.WrapRows(rows, normalizeError), nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args ...any) (liteorm.Result, error) {
	res, err := c.gdb.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, normalizeError(err)
	}
	return sqladapter.WrapResult(res), nil
}

func (c *conn) Begin(ctx context.Context) (liteorm.Tx, error) {
	return c.BeginTx(ctx, liteorm.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts liteorm.TxOptions) (liteorm.Tx, error) {
	return sqladapter.BeginTx(ctx, c.gdb.DB, sqlgen.SQLite, normalizeError, opts)
}

// Close drains the pool and releases any gosqlite VFS handles.
func (c *conn) Close() error { return c.gdb.Close() }

// Stats reports the database/sql connection-pool statistics of the underlying
// gosqlite pool, satisfying liteorm.StatsProvider.
func (c *conn) Stats() liteorm.PoolStats {
	s := c.gdb.Stats()
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

// normalizeError maps SQLite constraint/lock errors to liteorm sentinels using
// the driver's EXTENDED result codes (gosqlite's Code() masks to the primary
// code, so ExtendedCode() is required to tell unique/FK/not-null/check apart).
// Dual-wraps so both errors.Is (sentinel) and errors.As (driver error) stay
// reachable.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var ec interface{ ExtendedCode() int }
	var co interface{ Code() int }
	var ext int
	switch {
	case errors.As(err, &ec):
		ext = ec.ExtendedCode()
	case errors.As(err, &co):
		ext = co.Code()
	default:
		return err
	}
	switch ext {
	case 2067, 1555: // SQLITE_CONSTRAINT_UNIQUE / _PRIMARYKEY
		return fmt.Errorf("%w: %w", liteorm.ErrUniqueViolation, err)
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return fmt.Errorf("%w: %w", liteorm.ErrForeignKey, err)
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		return fmt.Errorf("%w: %w", liteorm.ErrNotNull, err)
	case 275: // SQLITE_CONSTRAINT_CHECK
		return fmt.Errorf("%w: %w", liteorm.ErrCheck, err)
	}
	switch ext & 0xff {
	case 5, 6: // SQLITE_BUSY / SQLITE_LOCKED
		return fmt.Errorf("%w: %w", liteorm.ErrDeadlock, err)
	}
	return err
}

// Compile-time checks that the adapters satisfy the core contracts.
var (
	_ liteorm.Querier       = (*conn)(nil)
	_ liteorm.Beginner      = (*conn)(nil)
	_ liteorm.StatsProvider = (*conn)(nil)
	_ liteorm.Querier       = (*pinnedConn)(nil)
	_ liteorm.Beginner      = (*pinnedConn)(nil)
)
