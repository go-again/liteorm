# AGENTS.md

Onboarding for AI agents and humans **developing** this repository. Read it top-to-bottom to be productive within a few minutes. (Agents *using* liteorm as a dependency want [`skills/`](skills/) instead; end users want [`README.md`](README.md) and [`docs/`](docs/index.md).)

---

## What this is

liteorm is a Go data-access library with **two front-ends over one driver-free core**:

- **`query`** — an explicit, generics-first SQL builder (`Select[T]`, typed `Col[V]` predicates, a CRUD `Repo[T]`, `Raw[T]`).
- **`orm`** — a declarative, tag-driven layer (models, `AutoMigrate`, associations, hooks, soft-delete) built on the same core.

Both run against four backends — SQLite (CGo-free, via the sibling `gosqlite.org`), Postgres (native pgx), MySQL, and SQL Server — and surface the same normalized errors.

Supported Go: the **two most recent releases** (the pin lives in `go.mod`; never name versions in prose). Modern idioms — generics, `iter.Seq2`, `log/slog`, `strings.SplitSeq`, `reflect.TypeFor` — are used freely; `just lint` runs `gopls modernize`.

## Architecture in one paragraph

The **core module `liteorm.org` carries zero database drivers**. It defines lean contracts (`Querier`, `Rows`, `Result`, `Beginner`, `Tx`, `Session` in `querier.go`/`db.go`), demand-driven capability interfaces (`LastInsertIder`, `BulkInserter`, `Returner` in `capability.go`), normalized error sentinels (`errors.go`), a `Dialect` contract with a `Feature` bitset (`dialect/`), an internal SQL generator (`internal/sqlgen`), a reflect-cached scanner (`internal/scan`), and the two front-ends (`query/`, `orm/`), plus `migrate/` and `gen/`. Each **backend is its own module** (`dialect/sqlite`, `dialect/postgres`, `dialect/mysql`, `dialect/mssql`) that implements the core contracts and is wired in at `Open` time, so the core's dependency graph stays driver-free. A `go.work` ties the modules together for local development.

## Repository layout

**Core module `liteorm.org` (driver-free):**

```
querier.go        Querier / Rows / Result / Beginner / Tx / Session contracts
db.go             *DB and *BoundTx (both are Session)
capability.go     LastInsertIder / BulkInserter / Returner (demand-driven)
errors.go         normalized error sentinels
dialect/          Dialect contract + Feature bitset + Field + Introspector
internal/sqlgen/  the SQL generator: Select/Insert/Update/Delete + 4 dialect renderers
internal/scan/    Scan[T] plan cache + iter.Seq2 streaming + shared tag resolver
internal/sqladapter/  shared database/sql → core adapter (sqlite/mysql/mssql)
query/            Select[T], predicates, subqueries, JSON/array ops, Repo[T], Raw[T]
orm/              schema, AutoMigrate, Repo, relations, Load, hooks, soft-delete, introspect
migrate/          the migration runner (Load, Up/Down, WritePair, splitStatements)
gen/              codegen: typed columns, models, SQL→Go, the gorm porter
```

**Separate modules (own `go.mod`, local `replace` directives):**

- `dialect/sqlite` — wraps the sibling `gosqlite.org`; `/search` (vector + FTS5 + RRF) and `/changeset` (SESSION ext) are sub-packages of this module so they add no deps to plain-backend users.
- `dialect/postgres` — native pgx; `listen.go` adds LISTEN/NOTIFY.
- `dialect/mysql`, `dialect/mssql` — over `internal/sqladapter`.
- `conformance/` — the cross-dialect test suite (one scenario set, run against every backend; live DBs gated behind `LITEORM_*_DSN`).
- `cmd/sqlc-gen-liteorm/` — a sqlc process plugin; `plugin/codegen.pb.go` is **vendored generated protobuf** (MIT, from sqlc) — do not hand-edit it.
- `studio/` — the embedded database studio (admin GUI) as a stdlib `http.Handler`. The library package depends only on the driver-free core (you pass it an opened `*liteorm.DB`); its tests + `cmd/studio-demo` pull in `dialect/sqlite`. The frontend lives in `studio/web/` (a Vite app importing the Prisma Studio UI, driven by a thin adapter in `studio/web/src/adapter.ts`); the **built `studio/web/dist` is a committed, `go:embed`-ed artifact — do not hand-edit it**, run `just studio-ui` to regenerate. The UI-upgrade procedure is `dev/studio-ui-upgrade.md`.
- `examples/*` — runnable, smoke-tested programs, each its own module.

Dot-prefixed top-level dirs (`.sqlite/`) are local-only working state, gitignored; nothing in the module references them.

## Fragile invariants you must not break

1. **The core stays driver-free.** `liteorm.org` (root) must import no backend and no driver. Backends depend on the core, never the reverse. A feature that needs a driver type lives in that backend's module (and exposes it via a backend accessor like `sqlite.Conn`/`sqlite.Pin` or `postgres.Pool`), never on the core's public surface.
2. **Placeholders are rendered late.** Predicates and builder fragments emit `?` markers; `internal/sqlgen` renumbers every `?` to the dialect's placeholder (`$n` / `?` / `@pN`) in one global pass at `Build` time. Never bake a dialect-specific placeholder into a predicate or a fragment.
3. **Dialect-specific operators are `Feature`-gated.** JSONB/array operators and any non-portable SQL carry a `Predicate.feat` (or check `d.Features()`), enforced in `query.resolved()` *before* rendering, so an unsupported operator fails at build time, not as opaque SQL. Add a `Feature` flag (append to the const block to keep bit positions stable) rather than dialect name checks.
4. **Capability interfaces are demand-driven.** A backend implements `LastInsertIder`/`BulkInserter`/`Returner` only when it can; front-ends type-assert and fall back. Don't widen a core interface to add an optional capability.
5. **Soft-delete uniqueness is dialect-aware.** A unique column under soft-delete gets a *partial* unique index (SQLite/Postgres/SQL Server) or a *functional* index (MySQL, which lacks partial indexes). Keep both paths.
6. **The sqlc plugin's protobuf is vendored.** `cmd/sqlc-gen-liteorm/plugin/codegen.pb.go` is generated; regenerate or re-vendor it, never edit by hand. `just modernize` skips `.pb.go`/`.gen.go` (like golangci skips generated files).
7. **Example generator helpers are not named `main.go`.** The `examples` recipe runs every `examples/**/main.go`; a codegen helper (e.g. `examples/queries/generate/`) uses `gen.go` so the runner skips it while `go generate` still compiles it.

## Conventions

- **Comments describe the contract for a consumer — never internal provenance.** No references to internal plans, phases, research, or design lineage; no "ported from X" / competitor-framing. A comment says *what the code does and the invariant it holds*. Keep planning context in the gitignored `.sqlite/` only.
- **Committed tooling is generic.** The `justfile` and any committed script must not assume a personal environment.
- **`interface{}` is `any`.** Always.
- **Markdown:** never hard-wrap prose (one long line per paragraph); no version numbers in prose; no "recent additions" / changelog holding sections — content goes in its feature-section home.
- **errcheck** is excluded for `_test.go` and `examples/`; new library code is fully checked — don't smuggle logic into an excluded path.
- **Git:** never commit or push without explicit per-action authorization; commit messages describe the diff and carry no AI/tool trailers.

## Common tasks

| Task | Command |
|---|---|
| Build / test / lint | `just build` · `just test` · `just lint` |
| One named test | `just test-one TestHybridFusion` |
| Race detector | `just test-race` |
| Format check / apply | `just fmt-check` / `just fmt` |
| Run / smoke-test examples | `just example <name>` / `just examples` |
| Live cross-dialect suite | `just db-up` then `just test-live` (any Docker engine) |
| Full CI locally | `just ci` |
| List recipes | `just --list` |

`just` is convenience over vanilla `go test ./...`; the multi-module sweeps iterate `go.work`.

## When asked to add a feature

First: **which layer owns it?**

- **New SQL shape** (a clause, set op, locking) → `internal/sqlgen` (model + render across all four dialects + a `Feature` flag if non-portable), then surface it on the `query` builder.
- **New typed operator / predicate** → `query/predicate.go` (or `query/jsonarray.go` / `query/subquery.go`); gate non-portable ones via `Predicate.feat`.
- **ORM behavior** (a Repo method, association, hook, migration rule) → `orm/`.
- **A backend capability** → that backend's module, exposed via a capability interface + a backend accessor; never leak the driver type into the core.
- **Codegen** → `gen/` (and the sqlc plugin in `cmd/sqlc-gen-liteorm/` if it's a sqlc-side concern).

Always:

1. Add tests next to the code, and a `conformance` scenario if it touches SQL the four backends must agree on — run it live (`just db-up && just test-live`).
2. `just lint` and `just test` green before reporting done.
3. **Update the docs the change touches, in the same change:** the package `doc.go` / doc comments (pkg.go.dev), the relevant `docs/guides|reference/*.md`, the matching `skills/<name>/SKILL.md` (skills ship to consumers and go stale silently — treat them as part of the feature), and `README.md` only if the landing surface changes. Don't quote test counts in user-facing docs.

## Where to look for what

| Question | File |
|---|---|
| Core contracts / capabilities | `querier.go`, `capability.go`, `db.go` |
| Error normalization | `errors.go` + each backend's `normalizeError` |
| SQL rendering / placeholder renumbering | `internal/sqlgen/sqlgen.go` (Select) · `mutate.go` (Insert/Update/Delete) · `dialects.go` (4 renderers) |
| Typed predicates / JSON / array / subquery | `query/predicate.go` · `jsonarray.go` · `subquery.go` |
| Row scanning / tag resolution | `internal/scan/scan.go` · `tags.go` |
| ORM schema / tags / relations | `orm/schema.go` · `orm/relations.go` |
| Eager load (N+1-safe) / m2m attach | `orm/load.go` |
| Hooks | `orm/hooks.go` |
| AutoMigrate / introspection / diff | `orm/migrate.go` · `orm/introspect.go` |
| Migration runner / file formats | `migrate/migrate.go` · `migrate/source.go` · `migrate/emit.go` |
| Codegen modes | `gen/gen.go` (columns/models) · `gen/queries.go` (SQL→Go) · `gen/gormport.go` (gorm porter) |
| sqlc plugin | `cmd/sqlc-gen-liteorm/{main,generate,types}.go` |
| SQLite vector/FTS/hybrid · changesets | `dialect/sqlite/search/search.go` · `dialect/sqlite/changeset/changeset.go` |
| SQLite backend / accessors | `dialect/sqlite/sqlite.go` (`Open`, `OpenEncrypted`, `Conn`, `Pin`) |
| Postgres LISTEN/NOTIFY | `dialect/postgres/listen.go` |

## Things that look broken but aren't

- **Predicates render `?`, not `$1`** — `internal/sqlgen` renumbers them per dialect at the end. By design.
- **`orm.Repo.Find(ctx)` takes no predicates** — orm reads are scope-only; complex filtering drops to `query.Select` on the same Session. Intentional.
- **`sqlgen.Select.Joins` is `[]Expr`, not `[]string`** — so join `ON` clauses can carry bound args.
- **A subquery renders through a `?`-emitting dialect wrapper** (`query/subquery.go`) so the *outer* query renumbers its placeholders. Don't "fix" it to render `$n` directly.
- **`cmd/sqlc-gen-liteorm/plugin/codegen.pb.go` looks hand-written but is generated** — leave it; `just modernize` already skips it.

## Last words

When in doubt, find the nearest existing parallel — a sibling predicate, a backend adapter, a conformance scenario — and mirror its shape. The four-dialect render tests in `internal/sqlgen` and the cross-backend `conformance` suite are the safety net; keep them green and live.
