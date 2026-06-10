# Backends

LiteORM ships four backends. The query and orm front-ends are portable across all of them; what differs is how you open each one, its DSN shape, and which optional SQL capabilities it advertises. Each backend lives in its own `liteorm.org/dialect/...` package and returns a `*liteorm.DB` carrying the matching dialect.

## Opening each backend

### SQLite

The SQLite backend wraps the pure-Go driver gosqlite.org — no cgo. `Open` applies a production pragma preset (WAL, a busy timeout, foreign keys on).

```go
import "liteorm.org/dialect/sqlite"

db, err := sqlite.Open("app.db")                    // path, or ":memory:"
db, err := sqlite.OpenEncrypted("secret.db", key)   // 32-byte key, Adiantum at rest
db, err := sqlite.OpenConfig(cfg)                    // full gosqlite.Config: custom VFS, pragmas, pooling
```

The DSN is a filesystem path (or `:memory:`). SQLite is the only backend with the advanced search and changeset extensions — see [SQLite search](../guides/sqlite-search.md) and [SQLite changesets](../guides/sqlite-changeset.md).

**At-rest encryption.** `sqlite.OpenEncrypted` opens an encrypted database using the default Adiantum cipher with a 32-byte key; the on-disk file is ciphertext and reopens only with the same key. Encryption refuses `:memory:` and is mutually exclusive with a custom VFS; the recommended pragma preset still applies.

```go
key := make([]byte, 32) // supply real 32-byte key material
db, err := sqlite.OpenEncrypted("secret.db", key)
```

### Postgres

The Postgres backend runs over the native pgx/v5 API (pgxpool), giving the binary protocol scan path and native fast paths.

```go
import "liteorm.org/dialect/postgres"

db, err := postgres.Open(ctx, "postgres://user:pass@host:5432/dbname?sslmode=disable")
```

The DSN is a libpq connection string or URL, as accepted by pgx. See [Postgres features](../guides/postgres.md) for LISTEN/NOTIFY, JSONB, arrays, and bulk insert.

### MySQL

The MySQL backend runs over go-sql-driver/mysql via `database/sql`.

```go
import "liteorm.org/dialect/mysql"

db, err := mysql.Open(ctx, "user:pass@tcp(host:3306)/dbname?parseTime=true")
```

The DSN is the go-sql-driver/mysql format. `Open` pings the database before returning.

### MSSQL

The MSSQL backend targets SQL Server.

```go
import "liteorm.org/dialect/mssql"

db, err := mssql.Open(ctx, "sqlserver://user:pass@host:1433?database=dbname")
```

## Capability matrix

Each dialect advertises optional capabilities as feature flags the query builder consults. The table below summarizes what each backend supports.

| Capability | SQLite | Postgres | MySQL | MSSQL |
| --- | --- | --- | --- | --- |
| Upsert clause | `ON CONFLICT` | `ON CONFLICT` | `ON DUPLICATE KEY` | `MERGE` |
| Insert/update output | `RETURNING` | `RETURNING` | — | `OUTPUT` |
| `LastInsertId` | yes | — (use `RETURNING`) | yes | — (use `OUTPUT`) |
| Common table expressions | yes | yes | yes | yes |
| Native JSON column | yes | yes | yes | — |
| JSONB containment (`@>`, `<@`) | — | yes | — | — |
| Array column + operators | — | yes | — | — |
| Identity / autoincrement | `AUTOINCREMENT` | `SERIAL`/`IDENTITY` | `AUTO_INCREMENT` | `IDENTITY` |
| `OFFSET`/`FETCH` pagination | — (`LIMIT`/`OFFSET`) | — (`LIMIT`/`OFFSET`) | — (`LIMIT`/`OFFSET`) | yes |
| Row locking (`FOR UPDATE`/`SHARE`, `SKIP LOCKED`) | — | yes | yes | — |
| `DISTINCT ON` | — | yes | — | — |
| `INTERSECT` / `EXCEPT` | yes | yes | — | yes |
| `LATERAL` join | — | yes | — | — |
| `UPDATE … FROM` | yes | yes | — | yes |
| `LISTEN`/`NOTIFY` | — | yes | — | — |

A few notes on reading this table:

- **Upsert** — every backend supports an upsert, but the SQL differs: `ON CONFLICT ... DO UPDATE` (SQLite, Postgres), `ON DUPLICATE KEY UPDATE` (MySQL), `MERGE` (MSSQL). The query builder emits the right one.
- **Output** — `RETURNING` (SQLite, Postgres) and `OUTPUT` (MSSQL) let an insert or update return rows; MySQL has neither and uses `LastInsertId` for generated keys.
- **JSON vs JSONB** — a native JSON column (`FeatJSON`) is broader than the JSONB containment operators (`FeatJSONB`): SQLite and MySQL have JSON types but not the `@>` / `<@` operators, which are Postgres-only.
- **Row locking** — `FOR UPDATE`/`FOR SHARE` (with optional `SKIP LOCKED`/`NOWAIT`) on Postgres and MySQL 8+; SQLite has no row locks and MSSQL uses table hints instead, so neither advertises it.
- **INTERSECT / EXCEPT** — SQLite, Postgres, and MSSQL; left off MySQL (supported only since 8.0.31). **LATERAL** and **`DISTINCT ON`** are Postgres-only; **`UPDATE … FROM`** is everywhere except MySQL (which uses `UPDATE … JOIN`).
- **LISTEN/NOTIFY** — Postgres-only; see [Postgres features](../guides/postgres.md).

## See also

- [Dialects reference](dialects.md) — placeholder styles, identifier quoting, and the feature-flag concept.
- [Errors](../guides/errors.md) — each backend's native errors normalized to portable sentinels.
- Full API per package: [`sqlite`](https://pkg.go.dev/liteorm.org/dialect/sqlite), [`postgres`](https://pkg.go.dev/liteorm.org/dialect/postgres), [`mysql`](https://pkg.go.dev/liteorm.org/dialect/mysql), [`mssql`](https://pkg.go.dev/liteorm.org/dialect/mssql).
