# Observability

LiteORM logs every statement through `log/slog` (see [logging](logging.md)), and for tracing, metrics, or audit it exposes one seam: an **observer** invoked around every statement, on a `*liteorm.DB` and on any transaction started from it. LiteORM's own statement logging rides on the same event, so an observer sees exactly what the logger sees — the SQL, the bind arguments, the elapsed time, the rows affected, and the error.

An observer implements two methods:

```go
type Observer interface {
	BeforeQuery(ctx context.Context, ev *liteorm.QueryEvent) context.Context
	AfterQuery(ctx context.Context, ev *liteorm.QueryEvent)
}
```

`BeforeQuery` runs before the statement and may return a derived context carrying per-statement state — an open trace span, a start marker — which is threaded into the executed statement and back to `AfterQuery`. `AfterQuery` runs after, with the event's `Duration`, `Rows`, and `Err` filled in. Register observers at construction; they compose as an onion (before in order, after in reverse):

```go
db, _ := sqlite.Open("app.db", liteorm.WithObserver(metrics, tracing))
```

The `QueryEvent` carries `Op` (`liteorm.MsgQuery` for a query, `liteorm.MsgExec` for a statement), `SQL`, `Args`, `Start`, `Duration`, `Rows` (rows affected for an Exec; `-1` for a query), and `Err`.

## Metrics

`AfterQuery` is a natural metrics hook — count statements and observe latency, labelled by operation:

```go
type metrics struct {
	count   *prometheus.CounterVec
	latency *prometheus.HistogramVec
}

func (m metrics) BeforeQuery(ctx context.Context, _ *liteorm.QueryEvent) context.Context {
	return ctx
}

func (m metrics) AfterQuery(_ context.Context, ev *liteorm.QueryEvent) {
	status := "ok"
	if ev.Err != nil {
		status = "error"
	}
	m.count.WithLabelValues(ev.Op, status).Inc()
	m.latency.WithLabelValues(ev.Op).Observe(ev.Duration.Seconds())
}
```

## Tracing

For tracing, open a span in `BeforeQuery`, stash it on the returned context, and close it in `AfterQuery` — the context LiteORM threads between the two is what carries the span:

```go
type tracing struct{ tracer trace.Tracer }

func (t tracing) BeforeQuery(ctx context.Context, ev *liteorm.QueryEvent) context.Context {
	ctx, span := t.tracer.Start(ctx, ev.Op)
	span.SetAttributes(attribute.String("db.statement", ev.SQL))
	return ctx
}

func (t tracing) AfterQuery(ctx context.Context, ev *liteorm.QueryEvent) {
	span := trace.SpanFromContext(ctx)
	if ev.Err != nil {
		span.RecordError(ev.Err)
	}
	span.End()
}
```

Because the span lives on the context and that context is threaded into the statement's own execution, child spans started by the driver (or by your `AfterQuery` of an outer observer) nest correctly.

## Notes

- **Zero overhead when idle.** With no observer registered and statement logging off, the statement runs straight through with no event allocated.
- **Observers run regardless of log level.** They are independent of the `slog` logger — registering one does not require enabling debug logging, and logging does not require an observer.
- **Bind arguments are never redacted for observers.** `WithSQLArgs(false)` redacts arguments in the *log* only; an observer always sees the real `ev.Args`. If your observer forwards arguments to a tracing/metrics/audit sink, redact sensitive values in the observer itself.
- **Transactions inherit the DB's observers**, so a unit of work traces as one tree across its statements.

## See also

- [Logging](logging.md) — the built-in `slog` statement log that rides this same seam.
- API reference: [pkg.go.dev/liteorm.org](https://pkg.go.dev/liteorm.org) (`Observer`, `QueryEvent`, `WithObserver`).
