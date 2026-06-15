<div align="center">

# LiteORM

[**Docs**](docs/index.md) &nbsp;·&nbsp; [**Get started**](docs/getting-started.md) &nbsp;·&nbsp; [**API reference**](https://pkg.go.dev/liteorm.org) &nbsp;·&nbsp; [**Agent skills**](skills/)

[![Go Reference](https://pkg.go.dev/badge/liteorm.org.svg)](https://pkg.go.dev/liteorm.org)

</div>

**Lite by design, not by omission.** LiteORM is *lite* because it's **modern**, not minimal — built from a clean Go baseline (generics, `iter.Seq2`, `log/slog`, CGo-free pure-Go SQLite) instead of a decade-old codebase patched a thousand times. Being lean is what lets it ship *more*, not less.

One library, **two front-ends over one core**: an explicit, generics-first **`query`** builder *and* a declarative, convention-driven **`orm`** — same backends, same transaction, same normalized errors. Use `orm` for CRUD and drop to `query` for a hot path on the *same* connection. SQLite is CGo-free (via [gosqlite.org](https://pkg.go.dev/gosqlite.org)); Postgres, MySQL, and SQL Server are first-class too. And it's **built for how software gets written now** — the first Go ORM with an embedded [database studio](docs/guides/studio.md) that has **AI built in**, plus [**Agent Skills**](skills/) so your AI assistant writes correct LiteORM on the first try.

```go
import (
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

db, _ := sqlite.Open("app.db")
defer db.Close()

books, _ := query.Select[Product](db).
	Filter(query.And(
		query.Col[string]("category").Eq("books"),
		query.Col[float64]("price").Lt(40),
	)).
	OrderBy("price").All(ctx)
```

## Why LiteORM

Most libraries that call themselves *lite* got there by leaving features out. LiteORM got there by never taking on the weight — no decade of reflection-heavy runtime, no `interface{}` plumbing, no mandatory CGo, no API grown by accretion. That's what frees it to do more, not less:

- **🧩 Two paradigms, one core — no library lock-in.** The `query` builder (typed, low-magic) and the `orm` (declarative, tag-driven) share one `Session`, one dialect, one scanner, and one set of normalized errors. A row fetched via `orm` feeds `query` on the *same* transaction. Most stacks make you pick a camp; LiteORM lets each part of your app pick the right tool.

- **⚡ Modern baseline, lean core.** Generics-first: typed result rows and typed `Col[V]` predicates, with no reflection on the hot path. The core module pulls in **no** database drivers — each backend (`dialect/sqlite`, `dialect/postgres`, `dialect/mysql`, `dialect/mssql`) is its own module, so your build only carries the driver you use. SQLite is pure Go: cross-compile with plain `go build`, ship static distroless/alpine binaries, no C toolchain. `iter.Seq2` streaming, `log/slog` logging, and `gopls modernize` enforced in CI keep it from drifting backward.

- **🤖 Built for agents — AI-native two ways.** The embedded [**studio**](docs/guides/studio.md) turns plain English into SQL through one server-side `WithAI` hook to any model, so your API key never reaches the browser — no other Go ORM ships this. *Separately*, the repo ships [**Agent Skills**](skills/) and an [`AGENTS.md`](AGENTS.md) so an AI coding assistant writes correct LiteORM without guessing. AI for the people *using* your database and the people *building* against it.

- **🎯 Safe by default.** No implicit lazy loading — eager `Load` is N+1-safe by construction. Soft-delete is an explicit tri-state scope, not a magic global filter. Constraint and not-found errors normalize to the *same* sentinels on every backend, so you write the check once.

- **⚙️ Codegen on-ramps when you want them.** Generate compile-time-safe typed columns, models from a live database, or typed Go functions from annotated SQL — and there's a [sqlc plugin](docs/guides/codegen.md) and a [gorm-tag porter](docs/guides/codegen.md). Opt in per need; the runtime path never requires codegen.

## Documentation

- **[docs/](docs/index.md)** — guides and reference. Start at [Getting started](docs/getting-started.md).
- **[pkg.go.dev/liteorm.org](https://pkg.go.dev/liteorm.org)** — the Go API reference.
- **[`skills/`](skills/)** — task recipes for AI agents *using* LiteORM; **[`AGENTS.md`](AGENTS.md)** for agents *developing* it.

## What you get

- **[Query builder](docs/guides/query.md)** — `query.Select[T]` with typed `Col[V]` predicates (`And`/`Or`/`Not`, `In`/`Like`/`IsNull`), typed `Order` (`Asc`/`Desc`) and `GroupByCols`, typed aggregates (`Sum`/`Avg`/`Min`/`Max` and grouped `Into`), join helpers, set operations (`Union`/`Intersect`/`Except`), CTEs (`With`/`WithRecursive`) and subquery `From`/`JoinSub`, row locking (`ForUpdate`/`ForShare`/`SkipLocked`), `DistinctOn`, `IN`-subqueries and `EXISTS`, `Having`, `iter.Seq2` streaming, a CRUD `Repo` with `Upsert` and bulk insert, multi-row `Update`/`Delete` builders with `RETURNING` and correlated `UPDATE … FROM`, window functions (`RowNumber`/`Rank`/`Lag`/…) and scalar subqueries in the SELECT list, and a `Raw[T]` escape hatch.
- **[ORM](docs/guides/orm.md)** — declarative models from `orm:""` (or `gorm:""`) tags, additive `AutoMigrate`, a CRUD Repo, [associations](docs/guides/associations.md) (has-many / has-one / belongs-to / many-to-many) with **N+1-safe eager loading**, typed [hooks](docs/guides/hooks.md), and [soft delete](docs/guides/soft-delete.md) with a unique-index fix.
- **[Studio + AI](docs/guides/studio.md)** — the first Go ORM to ship an **embedded database studio**: a browser admin GUI (browse, filter, edit, follow foreign keys, run SQL, import/export CSV·JSON·SQL) as a stdlib `http.Handler` you mount behind your own auth. **AI is built in** — natural-language → SQL, English filters, and automatic result charts via one server-side `WithAI` hook to any model. Backend is LiteORM-native (so it knows your relations and types); frontend is the Prisma Studio UI, embedded.
- **[Migrations](docs/guides/migrations.md)** — additive `AutoMigrate` plus a two-track model: destructive changes become a *reviewable* migration you apply through a thin runner that reads golang-migrate / goose / plain SQL files.
- **[Normalized errors](docs/guides/errors.md)** — `ErrUniqueViolation`, `ErrForeignKey`, `ErrNotNull`, `ErrCheck`, `ErrNoRows`, `ErrDeadlock`, `ErrSerialization` — the same on SQLite, Postgres, MySQL, and SQL Server.
- **[Code generation](docs/guides/codegen.md)** — typed `Column[V]` constants for compile-time column safety, models from a live DB, SQL→typed-Go from annotated queries, a sqlc process plugin, and a gorm→LiteORM tag porter.
- **[SQLite vector / full-text / hybrid search](docs/guides/sqlite-search.md)** — typed vector (sqlite-vec) and FTS5 search, plus reciprocal-rank-fusion **hybrid** search, keyed by your model's primary key; encryption at rest.
- **[SQLite changesets](docs/guides/sqlite-changeset.md)** — capture, apply, invert, and concat changesets for audit logs, one-way replication, and undo.
- **[Postgres extras](docs/guides/postgres.md)** — LISTEN/NOTIFY and typed JSONB (`->`, `->>`, `@>`) and array (`@>`, `&&`, `= ANY`) operators.

## How it compares

Read top-down: who LiteORM is, what it's built for, then the capability depth behind it.

| Capability | LiteORM | gorm | bun | sqlc | ent |
|---|---|---|---|---|---|
| Core runtime model | generics, no reflection | reflection | reflection | generated code | generated code |
| Explicit query builder **and** declarative ORM in one lib | ✓ | ✗ (ORM) | ✓ builder (light ORM) | ✗ (SQL→Go) | ✗ (generated ORM) |
| CGo-free SQLite (pure Go, no C toolchain) | ✓ | driver of choice | driver of choice | driver of choice | driver of choice |
| Ships an **embedded database studio** (admin GUI) in-tree | ✓ | ✗ | ✗ | ✗ | ✗ |
| Studio with **built-in AI** (NL→SQL, English filters, result charts) | ✓ | ✗ | ✗ | ✗ | ✗ |
| Ships AI Agent Skills + task-oriented docs | ✓ | ✗ | ✗ | ✗ | ✗ |
| Typed joins, set ops, subqueries, CTEs, window functions | ✓ | partial | partial | n/a | ✓ |
| Associations + **N+1-safe** eager load | ✓ (explicit, no lazy) | ✓ (lazy preload) | ✓ | ✗ | ✓ |
| Normalized errors across all backends | ✓ | ✗ | ✗ | ✗ | ✗ |
| Migrations: additive auto **+** reviewable destructive | ✓ | ✓ (auto only) | ✓ (files) | ✗ | ✓ |
| Codegen: typed columns / models / SQL→Go + sqlc plugin | ✓ | ✗ | ✗ | ✓ (SQL→Go) | ✓ (schema→code) |
| SQLite vector + FTS5 + hybrid search, changesets | ✓ | ✗ | ✗ | ✗ | ✗ |

**Where it's strongest:** LiteORM is the only Go library that puts a typed query builder *and* a declarative ORM over one core — so the wins above (normalized errors on every backend, N+1-safe-by-construction loading, CGo-free SQLite with vector/FTS/hybrid search and changesets, an in-tree database studio with built-in AI, shipped Agent Skills) hold across both front-ends, not just one.

**By design:** LiteORM is runtime-first — typed predicates and clauses cover the everyday SQL surface, with `Project` / `Raw[T]` as the escape hatch for the genuinely exotic, rather than a fully-generated DSL like ent. Full compile-time *column* safety is therefore opt-in through the [codegen](docs/guides/codegen.md) on-ramp instead of mandatory, and the ecosystem is younger than gorm's or ent's.

## Quick start

```go
import (
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

db, _ := sqlite.Open("app.db")
defer db.Close()

// Declarative: tag a model, migrate, CRUD.
type Author struct {
	ID    int64
	Name  string
	Email string `orm:"email,unique"`
}
func (Author) TableName() string { return "authors" }

_ = orm.AutoMigrate[Author](ctx, db)
authors := orm.NewRepo[Author](db)
ada := Author{Name: "Ada", Email: "ada@example.com"}
_ = authors.Create(ctx, &ada)

// Explicit: typed predicates on the same DB.
hits, _ := query.Select[Author](db).
	Filter(query.Col[string]("email").Like("%@example.com")).
	OrderBy("name").All(ctx)
```

Full walkthrough in **[Getting started](docs/getting-started.md)**.

## Packages

| Import path | What it gives you |
|---|---|
| `liteorm.org` | core: `DB` / `Session` / `Tx`, normalized error sentinels, capability interfaces (driver-free) |
| `liteorm.org/query` | the explicit builder: `Select[T]`, typed predicates, `Repo[T]`, `Raw[T]` |
| `liteorm.org/orm` | the declarative ORM: models, `AutoMigrate`, `Repo[T]`, associations, hooks, soft-delete |
| `liteorm.org/migrate` | the migration runner (`Load`, `Up`/`Down`, `WritePair`) |
| `liteorm.org/gen` | codegen: typed columns, models, SQL→Go, the gorm porter |
| `liteorm.org/dialect/sqlite` | CGo-free SQLite backend (+ `/search`, `/changeset` sub-packages) |
| `liteorm.org/dialect/postgres` | native pgx backend (+ LISTEN/NOTIFY) |
| `liteorm.org/dialect/mysql` · `liteorm.org/dialect/mssql` | MySQL · SQL Server backends |
| `liteorm.org/cmd/sqlc-gen-liteorm` | a sqlc codegen plugin that emits LiteORM runtime code |

## Migrating

| Coming from | Path |
|---|---|
| **gorm** | your `gorm:"..."`-tagged models work in `orm` unchanged; run [`gen.PortSource`](docs/guides/codegen.md) to rewrite them to native `orm:"..."` tags and drop the gorm dependency. Differences: eager loading is explicit (`Load`), soft-delete uses tri-state scopes. |
| **sqlc** | keep your annotated `.sql` files — the [`sqlc-gen-liteorm`](docs/guides/codegen.md) plugin emits LiteORM runtime functions from them; or use LiteORM's own annotated-SQL generator. |
| **database/sql** | open a backend, then use `query.Raw[T]` for existing SQL and adopt the builder / ORM incrementally on the same `*sql`-backed connection. |

## Examples

Runnable, smoke-tested programs under [`examples/`](examples/): `blog` (a small end-to-end blog engine — models, associations, nested eager loading, an aggregate query, and a transaction), `query` and `orm` feature showcases, `logging` (statement tracing), `search` (vector + full-text + hybrid), `queries` (SQL→Go codegen), `gormport` (the gorm porter), and `codegen`. `just example <name>` runs one; `just examples` runs them all.

## Development & contributing

`just` recipes drive everything: `just` (build + test + lint), `just test`, `just test-race`, `just lint`, `just examples`, and `just db-up` / `just test-live` for the live cross-dialect conformance suite (any Docker engine). Architecture, invariants, conventions, and where-to-look live in **[`AGENTS.md`](AGENTS.md)**.

## Supported Go

The two most recent Go releases; the exact pin lives in `go.mod`. See [Supported Go](docs/reference/supported-go.md).

## Sponsors

This project is supported by:

- **[ssh2incus](https://ssh2incus.com)** — an open-source SSH server that connects directly to [Incus](https://linuxcontainers.org/incus/) containers and virtual machines, routing incoming SSH connections to the right instance via the Incus API.
- **[mobydeck](https://github.com/mobydeck)** — a GitHub organization publishing open-source developer tools and infrastructure utilities across Go, C, TypeScript, shell, and Ruby.

If your company benefits from this library and you'd like to be listed here, open an issue.

## License

Apache 2.0. See [LICENSE](LICENSE).
