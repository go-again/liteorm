package liteorm

import (
	"context"
	"log/slog"
	"slices"
	"time"
)

// QueryEvent describes one executed statement, handed to an Observer around its
// execution. The same event value is passed to BeforeQuery and AfterQuery, so an
// observer can stash state on the context in BeforeQuery and read the completed
// Duration / Rows / Err in AfterQuery. Op is MsgQuery for a query (SELECT or a
// RETURNING write read back) or MsgExec for a statement (INSERT/UPDATE/DELETE/DDL).
type QueryEvent struct {
	Op  string // MsgQuery or MsgExec
	SQL string // the SQL text, with the dialect's placeholders
	// Args is the bind arguments, always the real values — WithSQLArgs(false)
	// redacts arguments in the statement LOG only, not here. An observer that
	// forwards Args to a tracing/metrics/audit sink must redact sensitive values
	// itself.
	Args     []any
	Start    time.Time     // when execution began
	Duration time.Duration // how long it took (set before AfterQuery)
	Rows     int64         // rows affected for an Exec; -1 for a query
	Err      error         // the execution error, if any (set before AfterQuery)
}

// Observer wraps statement execution for tracing, metrics, or audit — the seam
// LiteORM's own statement logging rides on. BeforeQuery runs before the statement
// (observers in registration order) and may return a derived context carrying
// per-statement state (an open span, a timer); that context is threaded into the
// executed statement and back to AfterQuery. AfterQuery runs after the statement
// (in reverse order), with the event's Duration, Rows, and Err filled in. Return
// ctx unchanged from BeforeQuery when there is nothing to carry.
//
// Register observers with WithObserver. They fire for statements on a *DB and on
// any transaction started from it.
type Observer interface {
	BeforeQuery(ctx context.Context, ev *QueryEvent) context.Context
	AfterQuery(ctx context.Context, ev *QueryEvent)
}

// stmtObs is the per-handle observability config shared by *DB and *BoundTx: the
// registered observers plus the built-in slog logging, which consumes the same
// event last.
type stmtObs struct {
	log       *slog.Logger
	logArgs   bool
	observers []Observer
}

// active reports whether any observation is needed — an observer is registered,
// or statement logging is on. When false, the front door skips the event
// machinery entirely (the zero-overhead fast path).
func (o stmtObs) active(ctx context.Context) bool {
	return len(o.observers) > 0 || o.log.Enabled(ctx, slog.LevelDebug)
}

// begin builds the event and runs the before-chain in registration order,
// threading each observer's returned context forward.
func (o stmtObs) begin(ctx context.Context, op, query string, args []any) (context.Context, *QueryEvent) {
	ev := &QueryEvent{Op: op, SQL: query, Args: args, Start: time.Now(), Rows: -1}
	for _, ob := range o.observers {
		ctx = ob.BeforeQuery(ctx, ev)
	}
	return ctx, ev
}

// end fills in the result, runs the after-chain in reverse order (the onion), and
// then emits the built-in statement log from the same event.
func (o stmtObs) end(ctx context.Context, ev *QueryEvent, err error, rows int64) {
	ev.Duration = time.Since(ev.Start)
	ev.Err = err
	ev.Rows = rows
	for _, ob := range slices.Backward(o.observers) {
		ob.AfterQuery(ctx, ev)
	}
	if o.log.Enabled(ctx, slog.LevelDebug) {
		logStmt(ctx, o.log, ev.Op, ev.SQL, ev.Args, o.logArgs, ev.Duration, rows, err)
	}
}
