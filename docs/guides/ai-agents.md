# AI agents & skills

LiteORM is built for how software gets written now: through AI coding assistants. Rather than leave your assistant to guess the API from training data — and write subtly wrong code — LiteORM ships **Agent Skills**: task-scoped instruction files that hand the assistant the exact, current API for one job, so it gets it right the first time. If your day-to-day coding involves an AI assistant, setting this up is the highest-leverage five minutes you'll spend getting started.

There are three distinct pieces, for three different audiences:

- **Agent Skills** (`skills/`) — for an assistant writing application code **with** LiteORM. *This page.*
- **`AGENTS.md`** — for an agent (or human) contributing **to** the LiteORM repository itself.
- **AI in the studio** — for the people **using** your database: natural-language → SQL inside the embedded admin GUI. See the [studio guide](studio.md).

## What a skill is

A skill is a folder under [`skills/`](../../skills/) containing a single `SKILL.md`: YAML frontmatter (a `name` and a one-sentence `description` that states *when* to use it) followed by a tight, actionable body — the real API as tables and short snippets, plus the pitfalls that matter. Each skill covers one job (writing queries, defining models, migrating a schema, porting from gorm, …), so the assistant loads only what the current task needs.

The `description` is the trigger: a capable assistant reads the available skills' descriptions and pulls in the relevant `SKILL.md` on demand, the way it would consult documentation — except this documentation is written for a machine and pinned to the version of LiteORM you're using.

## Setting it up

Copy the `skills/` folder from the LiteORM repository into your AI assistant's skills (or instructions) directory — the whole folder, or just the individual skill folders you want. Assistants that support the Agent Skills convention (a `SKILL.md` with `name`/`description` frontmatter) then load the right skill automatically based on the task you describe.

If your assistant doesn't auto-load skills, the files are still useful by hand: paste the relevant `SKILL.md` into the conversation when you start a task — "writing a migration", "defining a model with relations", "porting a gorm struct" — and the assistant has the precise surface in front of it instead of a half-remembered one.

Either way, point your assistant at **[`skills/using-liteorm`](../../skills/using-liteorm/SKILL.md)** first: it covers choosing the `query` vs `orm` front-end, opening a backend, first CRUD, and the shared Session/transaction model — the orientation every other skill builds on.

## The skills

| Skill | Use it when you're… |
| --- | --- |
| [`using-liteorm`](../../skills/using-liteorm/SKILL.md) | Starting out: choosing `query` vs `orm`, opening a backend, first CRUD, the shared Session/transaction model. |
| [`query-builder`](../../skills/query-builder/SKILL.md) | Writing explicit, typed queries: predicates, joins, unions, subqueries, `EXISTS`, `Iter` streaming, `Pluck`, aggregates, `Raw`, the `query.Repo`. |
| [`orm-models`](../../skills/orm-models/SKILL.md) | Defining declarative models: structs + tags, `AutoMigrate`, the `orm.Repo`, associations (Load/Attach), hooks, soft delete. |
| [`migrations`](../../skills/migrations/SKILL.md) | Evolving a schema: additive `AutoMigrate`, reviewable `GenerateMigration`, the migrate runner, `WritePair`. |
| [`codegen`](../../skills/codegen/SKILL.md) | Generating typed columns/models/queries: `liteorm gen`, the sqlc plugin, the gorm porter. |
| [`sqlite-search`](../../skills/sqlite-search/SKILL.md) | Adding SQLite vector, full-text (FTS5), or hybrid (RRF) search. |
| [`postgres-advanced`](../../skills/postgres-advanced/SKILL.md) | Using Postgres LISTEN/NOTIFY or the typed JSONB / array operators. |
| [`porting-from-gorm`](../../skills/porting-from-gorm/SKILL.md) | Migrating a gorm codebase: native gorm-tag reading and rewriting to native `orm` tags, and what differs. |
| [`logging`](../../skills/logging/SKILL.md) | Tracing executed SQL while developing, via slog or the colored handler. |
| [`studio`](../../skills/studio/SKILL.md) | Mounting the embedded database studio, registering models, locking it down. |
| [`pitfalls`](../../skills/pitfalls/SKILL.md) | Avoiding the gotchas that trip people up. |

Skills ship alongside the code and are updated with it, so they stay in lockstep with the API — a skill won't recommend a method that no longer exists or miss one that was just added.

## Contributing to LiteORM with an agent

If your agent is working **inside** the LiteORM repository — adding a feature, fixing a bug — point it at [`AGENTS.md`](../../AGENTS.md) instead. That's the contributor onboarding: the architecture in a paragraph, the repository layout, the fragile invariants not to break (the driver-free core, late placeholder rendering, feature-gated dialect operators), the conventions, and the common `just` tasks. It's written to make an agent productive in a few minutes without re-deriving how the project is structured.

## See also

- [Getting started](../getting-started.md) — install and your first query/model.
- [Studio](studio.md) — the embedded admin GUI and its built-in natural-language-to-SQL.
- [`skills/`](../../skills/) — the skill files themselves; [`AGENTS.md`](../../AGENTS.md) — contributor onboarding.
