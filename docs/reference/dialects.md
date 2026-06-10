# Dialects

A dialect is the contract a backend implements so LiteORM's query builder can emit SQL for it. The contract is deliberately lean — the hot SQL-generation methods only — and optional SQL capabilities are expressed as feature flags rather than growing the interface. Most users never touch a dialect directly; this reference is for those who want to understand the differences the front-ends paper over.

## Placeholder styles

Each dialect writes bind-variable placeholders in its native style. The query builder asks the dialect to append the placeholder for parameter *n*, so the right syntax is emitted directly with no rewrite pass.

| Dialect | Placeholder for parameter *n* |
| --- | --- |
| SQLite | `?` |
| MySQL | `?` |
| Postgres | `$1`, `$2`, … |
| MSSQL | `@p1`, `@p2`, … |

When you write raw SQL for a backend (for example in a generated query or a hand-written statement), use that backend's placeholder style.

## Identifier quoting

Identifiers are quoted in the dialect's native style, with embedded quote characters escaped.

| Dialect | Quoting | Default schema |
| --- | --- | --- |
| SQLite | `"name"` | `main` |
| Postgres | `"name"` | `public` |
| MySQL | `` `name` `` | (none) |
| MSSQL | `[name]` | `dbo` |

## Feature flags

A dialect advertises a bitset of optional capabilities. The query builder consults these to choose a code path — and predicates gated to a feature (Postgres JSONB containment, array operators) refuse to build against a dialect that lacks it, so a mistake is a build-time failure rather than opaque SQL. The flags are:

- **RETURNING / OUTPUT** — return rows from an insert or update. SQLite and Postgres use `RETURNING`; MSSQL uses the T-SQL `OUTPUT` clause.
- **OnConflict / OnDuplicateKey / Merge** — the three upsert dialects. `ON CONFLICT ... DO UPDATE` (SQLite, Postgres), `ON DUPLICATE KEY UPDATE` (MySQL), `MERGE` (MSSQL).
- **OffsetFetch** — `OFFSET ... FETCH` pagination instead of `LIMIT`/`OFFSET` (MSSQL).
- **CTE** — `WITH` common table expressions (all four).
- **JSON** — a native JSON/JSONB column type exists.
- **JSONB** — Postgres binary-JSON containment operators (`@>`, `<@`); narrower than JSON, which only means a native JSON type exists.
- **Array** — native array column type and its operators (Postgres).
- **Identity** — `IDENTITY` / `SERIAL` / `AUTO_INCREMENT` autoincrement.
- **LastInsertID** — the driver returns a usable last-insert id (SQLite, MySQL).

See the [backends reference](backends.md) for the per-dialect matrix of which flags each backend sets.

## Notable quirks

Some dialect differences leak into behaviour you should be aware of:

- **MSSQL `OFFSET`/`FETCH` needs `ORDER BY`.** T-SQL's `OFFSET ... FETCH` pagination is only valid with an explicit `ORDER BY`. Order your query when paginating on MSSQL.
- **MySQL has no partial index.** Soft-delete unique constraints rely on a partial unique index (`... WHERE deleted_at IS NULL`) on SQLite, Postgres, and MSSQL. MySQL lacks partial indexes, so the schema sync uses a functional unique index instead to achieve the same effect — a soft-deleted row stops occupying the unique key.
- **SQLite `time.Time` wants a `TIMESTAMP`-affinity column.** A `time.Time` (or `sql.NullTime`) field maps to a column declared `TIMESTAMP` so the driver round-trips a real time value instead of returning a bare string. When you hand-write SQLite DDL for a time column, declare it with `TIMESTAMP` affinity.

## See also

- [Backends reference](backends.md) — opening each backend and the capability matrix.
- [Errors](../guides/errors.md) — how each dialect's native errors map to portable sentinels.
- Full API: [`liteorm.org/dialect`](https://pkg.go.dev/liteorm.org/dialect).
