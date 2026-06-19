# Statement logging & debugging

LiteORM logs every executed SQL statement so that during development you can watch the queries go by and trace each one back to the line of Go that issued it. Each log event carries the SQL, the bind arguments, how long it took, the rows affected (for writes), the originating source location, and the error if any.

Logging goes through a standard `*slog.Logger`, so you pick the output: a colored, human-readable handler for development, or any structured handler (JSON, text, or an OpenTelemetry bridge) for production. Statement events are emitted at `slog.LevelDebug`, so **logging is silent unless the logger is enabled for debug** — there is no overhead in production where the level is higher.

## Colored development output

The `liteorm.org/log` package is a ready-made handler tuned for SQL: one line per statement, colored by speed (green) / slow (yellow) / error (red), with the SQL, args, rows, and the `file:line` that issued it.

```go
import (
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	devlog "liteorm.org/log"
)

db, _ := sqlite.Open("app.db", liteorm.WithLogger(devlog.New(os.Stderr, nil)))
```

Output looks like:

```
15:04:05.123 [liteorm] 35µs  INSERT INTO "widgets" ("name","price") VALUES (?, ?) RETURNING "id" args=["Gear", 500] (examples/blog/main.go:58)
15:04:05.123 [liteorm] 14µs  SELECT "widgets"."id", "widgets"."name" FROM "widgets" WHERE "price" > ? args=[300] (examples/blog/main.go:60)
```

The `(examples/blog/main.go:58)` is the exact line in *your* code that ran the statement, shown **relative to the working directory** so it's short and clickable. Each bind value is delimited and strings are quoted (`args=["Gear", 500]`), so a value that contains spaces is never ambiguous. Configure the handler with `devlog.Options`:

```go
devlog.New(os.Stderr, &devlog.Options{
	Color:         true,                   // ANSI color (default true; forced off when NO_COLOR is set)
	SlowThreshold: 200 * time.Millisecond, // statements at/over this are highlighted yellow
	Level:         slog.LevelDebug,        // the floor; raise it to quieten
	AbsPath:       false,                  // true prints the absolute caller path instead of the working-dir-relative one
})
```

Color is on by default and disabled automatically when the `NO_COLOR` environment variable is set. The caller path is relative by default; set `AbsPath: true` to print the absolute path (the structured `caller` attribute below is always absolute).

## Structured logs (JSON / text)

For machine-readable logs, pass any standard slog handler — LiteORM emits the same events through it:

```go
jsonLog := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
db, _ := sqlite.Open("app.db", liteorm.WithLogger(jsonLog))
```

```json
{"level":"DEBUG","msg":"liteorm.query","sql":"SELECT count(*) FROM widgets","dur":16958,"args":null,"caller":"/home/me/app/main.go:75"}
```

The structured `caller` is the absolute path (unambiguous for machine consumption); the colored handler is the one that shortens it for humans.

The event uses the message strings `liteorm.query` / `liteorm.exec` and the attribute keys `sql`, `args`, `dur`, `rows`, `caller`, `err` — all exported as constants (`liteorm.MsgQuery`, `liteorm.AttrSQL`, …) so a custom slog handler can match and format them.

## Turning logging on and off

Logging is purely level-gated:

- **On** — give LiteORM a logger enabled for `slog.LevelDebug` (`devlog.New(...)` is at debug by default; for a standard handler set `Level: slog.LevelDebug`).
- **Off** — use any logger above debug (the default `slog.Default()` is at info), and statements are not logged and not timed.

So a common pattern is to enable debug logging via a flag or env var:

```go
var lg *slog.Logger
if os.Getenv("APP_DEBUG") != "" {
	lg = devlog.New(os.Stderr, nil)
} else {
	lg = slog.New(slog.NewJSONHandler(os.Stderr, nil)) // info+ only
}
db, _ := sqlite.Open("app.db", liteorm.WithLogger(lg))
```

## Argument values and secrets

By default the bind argument *values* are logged — that is what makes a statement traceable and reproducible. If some statements carry secrets and you log at debug in a sensitive environment, redact the values (only the count is logged) with `liteorm.WithSQLArgs(false)`:

```go
db, _ := sqlite.Open("app.db", liteorm.WithLogger(devlog.New(os.Stderr, nil)), liteorm.WithSQLArgs(false))
```

Large bind values are bounded automatically: a `[]byte` or `string` argument over 256 bytes is logged as a `<N bytes>` summary or a truncated preview, so a statement that binds a multi-megabyte blob or text never dumps the whole payload into the log. Streamed large-object content never appears at all — an `orm.LOB` field binds an id, not the bytes.

## What gets logged

Statements run through a `liteorm.Session` (a `*DB` or a transaction) are logged — that covers both the `query` and `orm` front-ends and raw `ExecContext`/`QueryContext`/`query.Raw`, including statements inside a transaction. A connection pinned with `sqlite.Pin` (for SESSION/changeset capture) inherits the database's logger and `WithSQLArgs` setting, so its statements log identically. Runnable demonstration: [`examples/logging`](../../examples/logging).
