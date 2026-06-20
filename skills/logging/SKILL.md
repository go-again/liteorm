---
name: logging
description: Use when you need to see/trace executed SQL while developing with liteorm, enable debug logging, or wire liteorm into structured (slog/JSON) logs.
---

# liteorm statement logging

liteorm logs every executed statement through a standard `*slog.Logger` at **debug level** (so it is silent unless the logger is debug-enabled). Each event carries the SQL, bind args, duration, rows affected, the issuing `file:line`, and any error.

## Colored dev output (watch queries, trace to your code)

```go
import (
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	devlog "liteorm.org/log"
)

db, _ := sqlite.Open("app.db", liteorm.WithLogger(devlog.New(os.Stderr, nil)))
```

→ `15:04:05.123 [liteorm] 35µs  INSERT INTO "widgets" (...) VALUES (?, ?) args=["Gear", 500] (examples/blog/main.go:58)`

The `(examples/blog/main.go:58)` is the exact line of your code that issued the statement, shown relative to the working directory; bind values are delimited and strings quoted so a value with spaces is unambiguous.

Options: `devlog.New(w, &devlog.Options{Color: true, SlowThreshold: 200*time.Millisecond, Level: slog.LevelDebug, AbsPath: false})`. Color is on by default and auto-disabled when `NO_COLOR` is set; `AbsPath: true` prints the absolute caller path (the structured `caller` attribute is always absolute).

## Structured logs

Pass any slog handler — same events, machine-readable:

```go
db, _ := sqlite.Open("app.db", liteorm.WithLogger(
	slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))))
```

Events: message `liteorm.query` / `liteorm.exec`; attributes `sql`, `args`, `dur`, `rows`, `caller`, `err` (exported as `liteorm.MsgQuery`, `liteorm.AttrSQL`, … for custom handlers).

## On / off / redact

| Want | Do |
|---|---|
| See all SQL | give a debug-enabled logger (`devlog.New(...)` is debug by default) |
| Silence in prod | use a logger at info+ (the default); nothing is logged or timed |
| Toggle by env | `if os.Getenv("APP_DEBUG") != "" { lg = devlog.New(os.Stderr, nil) }` |
| Hide arg values (secrets) | add `liteorm.WithSQLArgs(false)` (logs arg count only) |

Large bind values are bounded automatically: a `string` over 256 bytes truncates to a preview and a `[]byte` over 256 bytes logs as a `<N bytes>` summary, so a multi-megabyte blob/text is never dumped (an over-cap `[]byte` arrives as that summary string, so a custom handler shouldn't assume blob args stay `[]byte`). `orm.LOB` content never appears (it binds an id, not bytes). A `sqlite.Pin`-ned connection inherits the DB's logger and `WithSQLArgs`. Not logged (no bind values, below the session layer): savepoint control (`SAVEPOINT`/`RELEASE`/`ROLLBACK TO`) and Postgres `LISTEN`/`UNLISTEN`/`CopyFrom`.

## Pitfalls

- **Nothing appears?** The logger must be enabled for `slog.LevelDebug`. A standard `slog.NewJSONHandler(w, nil)` defaults to info — set `&slog.HandlerOptions{Level: slog.LevelDebug}`. `devlog.New` is debug by default.
- **Don't set `Level: slog.LevelInfo` on `devlog`** expecting statements to show — they are at debug, which is below info. The `devlog` zero `Level` already means debug.
- Logging covers both front-ends and raw SQL on any `Session`, including inside a transaction.

Deeper guide: [../../docs/guides/logging.md](../../docs/guides/logging.md) · API: https://pkg.go.dev/liteorm.org/log
