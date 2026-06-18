---
name: studio
description: Use when adding liteorm's embedded database studio (admin GUI) to an app — mounting the http.Handler, registering models for rich introspection, and locking it down.
---

# Studio

liteorm ships a database studio (a browser admin GUI) as a stdlib `http.Handler` you mount in your own server. It is a **separate module** — `go get liteorm.org/studio` — so it adds nothing to your build until you want it; no separate binary or service to run. Full docs live with the module ([`liteorm.org/studio`](https://pkg.go.dev/liteorm.org/studio)).

## Mount it

```go
import (
	"net/http"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/studio"
)

db, _ := sqlite.Open("app.db")
defer db.Close()

// Mount under any path; wrap with your own auth middleware.
http.Handle("/studio/", http.StripPrefix("/studio", studio.Handler(db,
	studio.WithModels(&User{}, &Post{}),
)))
```

`studio.Handler(db, opts...)` returns an `http.Handler`. It works with any backend you've opened (`*liteorm.DB`); it does not open a connection itself. Visit the mount path with a trailing slash (`/studio/`).

## Options

- `studio.WithModels(&User{}, &Post{}, ...)` — register your models so introspection is model-aware: belongs-to relations become navigable foreign keys, and Go types refine datatypes the catalog can't (a `bool` stored as SQLite `INTEGER` shows as a boolean). Optional but recommended; unregistered tables still work from the catalog.
- `studio.WithReadOnly()` — disable all writes (browse-only).
- `studio.WithDisableSQL()` — remove the raw SQL editor.
- `studio.WithAI(fn)` — enable AI features (NL filters, SQL generation, result charts, query analysis). `fn` is `func(ctx, studio.AIRequest) (string, error)` — LLM-agnostic; the studio passes the assembled prompt (schema context, no row data) and you return the model's text. The key stays server-side. See the guide for a Claude example.

## Rules

- **Authenticate it.** The studio has no built-in auth and exposes a raw-SQL editor. Always wrap the handler with your authentication middleware; never serve it unauthenticated on a public address. Use `WithReadOnly()` / `WithDisableSQL()` to narrow it.
- **It's a separate module:** `go get liteorm.org/studio` / `import "liteorm.org/studio"`. The library package depends only on the liteorm core; you pass it your already-opened `*liteorm.DB`.
- **Lock read-only for public demos.** Build with `-tags studio_readonly` to force read-only at **compile time** regardless of options — write endpoints refuse with a clear 403, import is off, and the raw-SQL editor refuses any non-read statement (including a write smuggled inside a `WITH …` CTE). `studio.Hardened()` reports it; `/api/config` shows `"hardened": true`. Seed the DB directly through liteorm *before* mounting — the lock only covers the HTTP surface.

## Common shape

```go
mux := http.NewServeMux()
// ... your app routes ...
mux.Handle("/admin/db/", requireAdmin(http.StripPrefix("/admin/db",
	studio.Handler(db, studio.WithModels(models...)))))
http.ListenAndServe(":8080", mux)
```
