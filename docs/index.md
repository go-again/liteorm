# LiteORM documentation

LiteORM is a Go data-access library with **two front-ends over one CGo-free core**: an explicit, generics-first **`query`** builder and a declarative, convention-driven **`orm`**. Both run against the same backends, the same transaction, and the same normalized errors — so you use `orm` for CRUD and drop to `query` for a hot path on the same connection without changing libraries.

New here? Start with **[Getting started](getting-started.md)**, then pick the front-end that fits the task. Coding with an AI assistant? Set it up first with **[AI agents & skills](guides/ai-agents.md)** so it writes correct LiteORM from the start. Coming from gorm? Map what you know in **[Coming from gorm](guides/from-gorm.md)**.

## Guides

**Start here**

- [Cheat sheet](reference/cheatsheet.md) — the whole API surface on one screen, each row linking to its guide.
- [Recipes](guides/recipes.md) — copy-paste solutions for the common tasks (pagination, bulk insert, upsert, streaming, retries…).
- [Coming from gorm](guides/from-gorm.md) — the verb-for-verb map and what's intentionally different.

**Querying & models**

- [Query builder](guides/query.md) — `query.Select[T]`, typed predicates, joins, unions, subqueries, streaming, the Repo, and the raw escape hatch.
- [ORM models](guides/orm.md) — declarative structs, tags, `AutoMigrate`, the Repo, conventions.
- [Associations](guides/associations.md) — has-many / has-one / belongs-to / many-to-many with N+1-safe eager loading.
- [Hooks](guides/hooks.md) — typed, context-first lifecycle hooks.
- [Soft delete](guides/soft-delete.md) — tri-state scopes and the unique-index behavior.
- [Transactions](guides/transactions.md) — savepoints and `query`↔`orm` interop on one transaction.

**Operations**

- [Migrations](guides/migrations.md) — additive `AutoMigrate`, reviewable diffs, and the migration runner.
- [Errors](guides/errors.md) — normalized sentinels that work the same across every backend.
- [Statement logging & debugging](guides/logging.md) — watch every executed SQL statement, traced to the Go line that issued it, via slog or a colored handler.
- [Code generation](guides/codegen.md) — typed columns, models from a live database, SQL→typed-Go, the sqlc plugin, and the gorm porter.

**Backend-specific**

- [SQLite search](guides/sqlite-search.md) — vector, full-text, and hybrid (reciprocal-rank-fusion) search.
- [Large objects](guides/large-objects.md) — store files, uploads, and growing binary content as streamed `io.ReaderAt`/`io.WriterAt`, never loaded whole.
- [At-rest encryption](guides/encryption.md) — open an encrypted SQLite database; the on-disk file is ciphertext, readable only with the key.
- [SQLite changesets](guides/sqlite-changeset.md) — capture, apply, invert, and concat changesets for audit and replication.
- [Postgres](guides/postgres.md) — LISTEN/NOTIFY and the typed JSONB / array operators.

**Tooling**

- [Studio](guides/studio.md) — the embedded database studio: a browser admin GUI mounted as an `http.Handler`.
- [AI agents & skills](guides/ai-agents.md) — set up your AI coding assistant with the shipped Agent Skills so it writes correct LiteORM the first time.

## Reference

- [Cheat sheet](reference/cheatsheet.md) — every Repo method, query terminal, error sentinel, and migrate verb at a glance.
- [Backends](reference/backends.md) — opening each backend, DSN shapes, and the per-dialect capability matrix.
- [Dialects](reference/dialects.md) — placeholder styles, identifier quoting, feature flags, and dialect quirks.
- [Supported Go](reference/supported-go.md) — the supported-release policy.

## API reference

The complete, always-current API is on [pkg.go.dev/liteorm.org](https://pkg.go.dev/liteorm.org) — and per package: [query](https://pkg.go.dev/liteorm.org/query), [orm](https://pkg.go.dev/liteorm.org/orm), [migrate](https://pkg.go.dev/liteorm.org/migrate), [gen](https://pkg.go.dev/liteorm.org/gen), [dialect/sqlite](https://pkg.go.dev/liteorm.org/dialect/sqlite).

## For AI agents

LiteORM ships first-class support for AI coding assistants — see **[AI agents & skills](guides/ai-agents.md)** for the full setup. In short: if you're an assistant building **with** LiteORM, load the task recipes in [`skills/`](../skills/); if you're working **inside** this repository, read [`AGENTS.md`](../AGENTS.md).
