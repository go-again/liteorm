# Studio

LiteORM ships an embedded **database studio** — a browser admin GUI for inspecting and editing any LiteORM-backed database — as a standard `net/http` handler you mount in your own server. It is a **separate module**, [`liteorm.org/studio`](https://pkg.go.dev/liteorm.org/studio), so it adds nothing to your build unless you ask for it:

```sh
go get liteorm.org/studio
```

```go
import (
	"net/http"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/studio"
)

db, _ := sqlite.Open("app.db")
defer db.Close()

// Mount anywhere; wrap with your own auth.
http.Handle("/studio/", http.StripPrefix("/studio", studio.Handler(db)))
http.ListenAndServe(":8080", nil)
```

Then open `http://localhost:8080/studio/`. `studio.Handler(db, opts...)` returns a plain `http.Handler`, so it drops into the stdlib mux, `chi`, `echo`, or `gin`, and you wrap it with the authentication middleware you already use.

## What it gives you

- **Works on every backend** — SQLite, Postgres, MySQL, SQL Server — with **no Go models required**: point it at a database your app already owns and it introspects tables, columns, types, primary keys, and foreign-key navigation from the live catalog (schema-wide, so it scales to hundreds of tables). Registering models with `WithModels` is purely additive.
- **Browse, filter, edit** — page/sort/filter the grid, full-table search, follow foreign keys, edit cells inline with type-aware editors, insert and delete rows.
- **SQL editor, import/export, system info** — run read or write SQL with a result grid; export and import CSV / JSON / SQL; inspect connection, server, and per-dialect database settings.
- **Theme** — a system / light / dark switch.
- **AI, opt-in** — natural-language filters, English-to-SQL, automatic result charts, and query analysis through one server-side `WithAI` hook to any model, so your API key never reaches the browser.

## Mount it safely

The studio is an admin surface with a raw-SQL escape hatch and ships no auth of its own — wrap it with your middleware and never expose it unauthenticated. Narrow it with `studio.WithReadOnly()` / `studio.WithDisableSQL()`, or lock it read-only at **compile time** with `-tags studio_readonly` for a public, unbreakable demo.

## Full documentation

Options, the AI hook, security guidance, and the per-dialect "plug into an existing database" demos live with the module — see the [`liteorm.org/studio` API reference](https://pkg.go.dev/liteorm.org/studio) and the [studio repository](https://github.com/liteorm/studio).
