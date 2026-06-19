---
name: pitfalls
description: Use when liteorm code behaves unexpectedly, or proactively before writing it, to avoid the common gotchas around loading, soft delete, types, and dialect-gated operators.
---

# liteorm pitfalls

The gotchas that trip people up, with the fix.

## Associations

- **No lazy loading.** Accessing `post.Comments` without first calling `orm.Load` gives a zero/empty value, not the rows. Load explicitly: `orm.Load[Post, Comment](ctx, sess, posts, "Comments")`. It is N+1-safe — exactly one query per call regardless of parent count.
- **Nested eager load is not a dotted path.** Chain `orm.Load` one level at a time (load posts' authors, then those authors' relation).
- **`orm.Attach` for m2m links** — inserting into a join table is a separate call; `Load` reads, `Attach` writes.

## Soft delete

- **Default reads exclude deleted rows.** Use `repo.IncludeDeleted()` or `repo.OnlyDeleted()` when you need them, and `ForceDelete` for a hard delete.
- **The column must be `sql.NullTime` with the `soft_delete` tag** — `DeletedAt sql.NullTime \`orm:"deleted_at,soft_delete"\``. A plain `time.Time`, a `*time.Time`, or a missing tag won't trigger soft-delete behavior.
- **Unique indexes on soft-delete models are partial** (`WHERE deleted_at IS NULL`), so a soft-deleted row frees its unique key for reuse. That's intended.

## Types on SQLite

- **A `time.Time` column needs TIMESTAMP affinity** in hand-written DDL: `added_at TIMESTAMP NOT NULL`. SQLite has no native date type, so the column type must carry the affinity for round-trips to work. AutoMigrate handles this; raw `CREATE TABLE` is on you.
- **Bools round-trip as 0/1** on SQLite (stored as `INTEGER`). A `bool` Go field maps to an `INTEGER NOT NULL` column; `Col[bool]("active").Eq(true)` works as expected — just don't expect a literal `TRUE`/`FALSE` type in the DDL.

## query.Raw and scalars

- **`query.Raw[T]` needs T to be a struct.** It maps rows into struct fields (use `db:"col"` tags for aliases). A single-column scalar will not scan into `int64`/`string` directly.
- **For a scalar result**, either scan manually (`sess.QueryContext` + `rows.Scan(&n)`), or use the codegen scalar path (a `:one`/`:many` query whose result type is `int64`/`string`/etc. emits a direct `rows.Scan`).

## Dialect-gated operators

- **JSONB `Contains` (`@>`) and all array operators are Postgres-only.** The query builder rejects them at BUILD time on other dialects (a clear error, not opaque SQL at the DB). Plain JSON path extraction (`JSON("c").Key("k").Eq(...)`) is the exception — it works on SQLite too.
- **Raw `Where("...")` fragments are not column-validated** — only typed `Filter` predicates are, so typos in a raw fragment fail at the database, not at build. Reach for a typed predicate first: `Eq`/`Like`/`HasPrefix`/`HasSuffix`/`Contains`/`In`/`EqCol`, `query.Match` (SQLite), `Update.Inc`/`Dec`, `OnConflict(...).DoNothing()`, and `ExistsField` cover most former raw fragments.

## Sessions & transactions

- **Pick one front-end's Session and pass a transaction to both.** Both `query` and `orm` take a `liteorm.Session`; `*liteorm.DB` and a `*BoundTx` both satisfy it. Write via `orm.NewRepo[T](tx)` and read via `query.Select[T](tx)` on the same tx freely.
- **Nested `tx.Begin(ctx)` is a savepoint** — its `Rollback` undoes only that savepoint, not the outer tx.

## Migrations

- **AutoMigrate is additive only** — it never drops columns or alters types. Destructive changes go through `orm.GenerateMigration` + the migrate runner.
- **A dirty migration ledger blocks `Up`/`Down`** — fix the DB by hand, then `Force` to the correct version.

## Hooks

- **A mis-signed hook is a compile error, not a silent no-ev** — hooks are typed on T. The signature is `func (t *T) BeforeCreate(ctx context.Context, ev *orm.Event[T]) error`. Returning an error aborts the operation.

## Deeper

- Guide: [../../docs/guides/query.md](../../docs/guides/query.md)
- API: https://pkg.go.dev/liteorm.org
