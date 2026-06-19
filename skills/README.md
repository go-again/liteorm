# liteorm Agent Skills

These are task-scoped instruction files for an AI coding assistant. Each skill is a folder containing a `SKILL.md` with the exact liteorm API for one job, so the assistant writes correct code on the first try instead of guessing the surface from training data.

## Format

Each `SKILL.md` begins with YAML frontmatter (`name` + a one-sentence `description` that states the trigger), then a tight, actionable body: tables, short snippets, the real API, and a pitfalls call-out where it matters. To use them, drop the `skills/` folder (or a single skill folder) into your AI agent's skills/instructions directory; the agent loads the relevant `SKILL.md` on demand based on the `description` trigger.

## Skills

| Skill | Use when |
| --- | --- |
| [using-liteorm](using-liteorm/SKILL.md) | Starting with liteorm: choosing the `query` vs `orm` front-end, opening a backend, first CRUD, the shared Session/transaction model. |
| [query-builder](query-builder/SKILL.md) | Writing explicit, typed queries: predicates, joins, Union, subqueries, EXISTS, Iter streaming, Raw, the `query.Repo`. |
| [orm-models](orm-models/SKILL.md) | Declarative models: structs + tags, AutoMigrate, the `orm.Repo`, associations (Load/Attach), hooks, soft delete. |
| [migrations](migrations/SKILL.md) | Evolving a schema: AutoMigrate (additive), GenerateMigration (reviewable), the migrate runner, WritePair. |
| [codegen](codegen/SKILL.md) | Generating typed columns/models/queries: `liteorm gen`, the sqlc plugin, the gorm porter. |
| [sqlite-search](sqlite-search/SKILL.md) | SQLite vector (sqlite-vec), full-text (FTS5), and hybrid RRF search. |
| [large-objects](large-objects/SKILL.md) | Storing large/growing binary content (files, uploads, blobs) in SQLite as streamed `io.ReaderAt`/`io.WriterAt` via an `orm.LOB` field. |
| [encryption](encryption/SKILL.md) | Opening a SQLite database with at-rest (transparent page-level) encryption: keys, reopening, constraints. |
| [compressed-database](compressed-database/SKILL.md) | Storing a whole SQLite database compressed on disk (archival, distribution, embedded `.db`): `OpenCompressed` and the snapshot-model trade-offs. |
| [postgres-advanced](postgres-advanced/SKILL.md) | Postgres LISTEN/NOTIFY, and JSONB / array typed operators. |
| [porting-from-gorm](porting-from-gorm/SKILL.md) | Migrating a gorm codebase: native gorm-tag reading and rewriting to native `orm` tags; what differs. |
| [logging](logging/SKILL.md) | Seeing/tracing executed SQL while developing: debug logging via slog or the colored handler, traced to your code. |
| [studio](studio/SKILL.md) | Adding the embedded database studio (admin GUI): mounting the `http.Handler`, registering models, locking it down. |
| [pitfalls](pitfalls/SKILL.md) | Avoiding the gotchas that trip people up. |
