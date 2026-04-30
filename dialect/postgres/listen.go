package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	liteorm "liteorm.org"
	"liteorm.org/internal/sqlgen"
)

// Pool returns the underlying *pgxpool.Pool for a liteorm.Session opened by this
// package, so Postgres-specific features (LISTEN/NOTIFY) can acquire a dedicated
// connection. The second result is false for any other backend. The pgx type
// stays out of the core: this lives in the Postgres backend, which owns it.
func Pool(sess liteorm.Session) (*pgxpool.Pool, bool) {
	qp, ok := sess.(interface{ Querier() liteorm.Querier })
	if !ok {
		return nil, false
	}
	c, ok := qp.Querier().(*conn)
	if !ok {
		return nil, false
	}
	return c.pool, true
}

// Notification is a delivered LISTEN/NOTIFY message. It is a liteorm value type —
// pgconn.Notification is copied into it so no driver type leaks.
type Notification struct {
	Channel string // the channel the payload arrived on
	Payload string // the NOTIFY payload (possibly empty)
	PID     uint32 // backend PID that issued the NOTIFY
}

// Notify sends an asynchronous notification on channel with an optional payload,
// using any pooled connection. Listeners on the same database receive it. The
// channel name is an identifier, so it is passed through pg_notify's first
// argument as a bound parameter (no identifier interpolation).
func Notify(ctx context.Context, sess liteorm.Session, channel, payload string) error {
	_, err := sess.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	return err
}

// Listener subscribes to one or more LISTEN channels on a single dedicated
// connection (LISTEN is connection-scoped, so it cannot use the pool's
// round-robin). Receive blocks until a notification arrives or ctx is cancelled.
// A Listener is not safe for concurrent use; run one Receive loop per Listener.
// Always Close it to UNLISTEN and return the connection to the pool.
type Listener struct {
	pc *pgxpool.Conn
}

// Listen acquires a dedicated connection from sess's pool and issues LISTEN for
// each channel. sess must be a *liteorm.DB opened by this package.
func Listen(ctx context.Context, sess liteorm.Session, channels ...string) (*Listener, error) {
	pool, ok := Pool(sess)
	if !ok {
		return nil, errors.New("liteorm/postgres: Listen requires a *liteorm.DB opened by dialect/postgres")
	}
	pc, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	l := &Listener{pc: pc}
	for _, ch := range channels {
		if err := l.Add(ctx, ch); err != nil {
			_ = l.Close(ctx)
			return nil, err
		}
	}
	return l, nil
}

// Add subscribes to an additional channel on the same connection.
func (l *Listener) Add(ctx context.Context, channel string) error {
	// LISTEN takes an identifier, not a parameter; quote it safely.
	_, err := l.pc.Exec(ctx, "LISTEN "+quoteIdent(channel))
	return err
}

// Remove unsubscribes from a channel.
func (l *Listener) Remove(ctx context.Context, channel string) error {
	_, err := l.pc.Exec(ctx, "UNLISTEN "+quoteIdent(channel))
	return err
}

// Receive blocks until the next notification arrives on a subscribed channel or
// ctx is done. The returned error is ctx.Err() on cancellation.
func (l *Listener) Receive(ctx context.Context) (Notification, error) {
	n, err := l.pc.Conn().WaitForNotification(ctx)
	if err != nil {
		return Notification{}, err
	}
	return Notification{Channel: n.Channel, Payload: n.Payload, PID: n.PID}, nil
}

// Close releases the dedicated connection back to the pool; pgx resets the
// connection on release, which clears its subscriptions. A Listener is not safe
// for concurrent use — do not call Close while a Receive is in flight.
func (l *Listener) Close(context.Context) error {
	if l.pc != nil {
		l.pc.Release()
		l.pc = nil
	}
	return nil
}

// quoteIdent double-quotes a Postgres identifier via the dialect's quoting (the
// single source of quoting truth) for use in LISTEN/UNLISTEN channel names.
func quoteIdent(s string) string { return string(sqlgen.Postgres.QuoteIdent(nil, s)) }
