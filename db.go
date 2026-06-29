package liteorm

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"liteorm.org/dialect"
)

// DB is the primary handle: a backend Querier paired with its Dialect and a
// logger. Constructed by a backend's Open (e.g. sqlite.Open), never globally —
// liteorm has no global default DB by design.
type DB struct {
	q         Querier
	d         dialect.Dialect
	log       *slog.Logger
	logArgs   bool
	observers []Observer
}

// Option configures a DB at construction.
type Option func(*DB)

// WithLogger sets the slog.Logger used for statement logging. liteorm logs every
// statement at debug level, so logging is silent unless l is enabled for
// slog.LevelDebug. Use a JSON/text handler for structured logs or the colored
// handler in liteorm.org/log for development.
func WithLogger(l *slog.Logger) Option {
	if l == nil { // a nil logger would panic on every statement's Enabled check
		l = slog.New(slog.DiscardHandler)
	}
	return func(db *DB) { db.log = l }
}

// WithSQLArgs controls whether bind-argument values are included in statement
// logs. Default true (values are shown, which is what makes a statement
// traceable); pass false to log only the argument count when values may be
// sensitive.
func WithSQLArgs(v bool) Option { return func(db *DB) { db.logArgs = v } }

// WithObserver registers observers invoked around every statement on the DB and
// on any transaction started from it — the seam for tracing, metrics, or audit.
// Observers run independently of statement logging (no log level gates them).
// Multiple WithObserver options accumulate, in registration order. Note an
// observer receives the real bind arguments in QueryEvent.Args regardless of
// WithSQLArgs — that flag redacts only the statement log; redact in the observer
// if it forwards arguments to a sensitive sink.
func WithObserver(o ...Observer) Option {
	return func(db *DB) { db.observers = append(db.observers, o...) }
}

// New wraps a backend Querier + Dialect into a DB. Backends call this from Open.
func New(q Querier, d dialect.Dialect, opts ...Option) *DB {
	db := &DB{q: q, d: d, log: slog.Default(), logArgs: true}
	for _, o := range opts {
		o(db)
	}
	return db
}

// Dialect returns the backend's SQL dialect.
func (db *DB) Dialect() dialect.Dialect { return db.d }

// Logger returns the configured logger.
func (db *DB) Logger() *slog.Logger { return db.log }

// LogArgs reports whether bind-argument values are included in statement logs
// (the WithSQLArgs setting). A backend that binds a DB to a specific connection
// propagates it alongside Logger, so the bound handle logs identically.
func (db *DB) LogArgs() bool { return db.logArgs }

// Querier exposes the underlying backend Querier (for capability type-asserts).
func (db *DB) Querier() Querier { return db.q }

// obs returns the DB's per-statement observability config.
func (db *DB) obs() stmtObs {
	return stmtObs{log: db.log, logArgs: db.logArgs, observers: db.observers}
}

// QueryContext runs a query, invoking any registered observers around it and
// logging it (with timing + source location) when the logger is debug-enabled.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (Rows, error) {
	o := db.obs()
	if !o.active(ctx) {
		return db.q.QueryContext(ctx, query, args...)
	}
	ctx, ev := o.begin(ctx, MsgQuery, query, args)
	rows, err := db.q.QueryContext(ctx, query, args...)
	o.end(ctx, ev, err, -1)
	return rows, err
}

// ExecContext runs a statement, invoking any registered observers around it and
// logging it (with timing, rows affected, and source location) when the logger
// is debug-enabled.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	o := db.obs()
	if !o.active(ctx) {
		return db.q.ExecContext(ctx, query, args...)
	}
	ctx, ev := o.begin(ctx, MsgExec, query, args)
	res, err := db.q.ExecContext(ctx, query, args...)
	rows := int64(-1)
	if res != nil {
		rows = res.RowsAffected()
	}
	o.end(ctx, ev, err, rows)
	return res, err
}

// Begin starts a transaction. Errors if the backend is not a Beginner.
func (db *DB) Begin(ctx context.Context) (*BoundTx, error) {
	return db.BeginTx(ctx, TxOptions{})
}

// BeginTx starts a transaction with options.
func (db *DB) BeginTx(ctx context.Context, opts TxOptions) (*BoundTx, error) {
	b, ok := db.q.(Beginner)
	if !ok {
		return nil, errors.New("liteorm: backend does not support transactions")
	}
	tx, err := b.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &BoundTx{tx: tx, d: db.d, log: db.log, logArgs: db.logArgs, observers: db.observers}, nil
}

// Stats returns connection-pool statistics if the backend exposes them (a
// StatsProvider). The second result is false for backends without a pool. Stats
// are a database-level property, so this lives on *DB, not on a transaction.
func (db *DB) Stats() (PoolStats, bool) {
	if sp, ok := db.q.(StatsProvider); ok {
		return sp.Stats(), true
	}
	return PoolStats{}, false
}

// Close closes the backend if it is an io.Closer.
func (db *DB) Close() error {
	if c, ok := db.q.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// BoundTx is a transaction bound to its dialect + logger, so the front-ends
// (which take a Session) work identically inside a transaction.
type BoundTx struct {
	tx        Tx
	d         dialect.Dialect
	log       *slog.Logger
	logArgs   bool
	observers []Observer
}

// obs returns the transaction's per-statement observability config.
func (t *BoundTx) obs() stmtObs {
	return stmtObs{log: t.log, logArgs: t.logArgs, observers: t.observers}
}

// Dialect returns the transaction's dialect.
func (t *BoundTx) Dialect() dialect.Dialect { return t.d }

// Logger returns the transaction's logger.
func (t *BoundTx) Logger() *slog.Logger { return t.log }

// QueryContext runs a query inside the transaction, under the same observers and
// logging as the DB it was started from.
func (t *BoundTx) QueryContext(ctx context.Context, query string, args ...any) (Rows, error) {
	o := t.obs()
	if !o.active(ctx) {
		return t.tx.QueryContext(ctx, query, args...)
	}
	ctx, ev := o.begin(ctx, MsgQuery, query, args)
	rows, err := t.tx.QueryContext(ctx, query, args...)
	o.end(ctx, ev, err, -1)
	return rows, err
}

// ExecContext runs a statement inside the transaction, under the same observers
// and logging as the DB it was started from.
func (t *BoundTx) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	o := t.obs()
	if !o.active(ctx) {
		return t.tx.ExecContext(ctx, query, args...)
	}
	ctx, ev := o.begin(ctx, MsgExec, query, args)
	res, err := t.tx.ExecContext(ctx, query, args...)
	rows := int64(-1)
	if res != nil {
		rows = res.RowsAffected()
	}
	o.end(ctx, ev, err, rows)
	return res, err
}

// Begin opens a nested transaction (a savepoint), uniform across backends.
func (t *BoundTx) Begin(ctx context.Context) (*BoundTx, error) {
	inner, err := t.tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &BoundTx{tx: inner, d: t.d, log: t.log, logArgs: t.logArgs, observers: t.observers}, nil
}

// Commit commits the transaction (or releases the savepoint).
func (t *BoundTx) Commit(ctx context.Context) error { return t.tx.Commit(ctx) }

// Rollback rolls back the transaction (or to the savepoint).
func (t *BoundTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// Compile-time checks that the handles satisfy Session.
var (
	_ Session = (*DB)(nil)
	_ Session = (*BoundTx)(nil)
)
