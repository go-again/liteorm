---
name: using-liteorm
description: Use when starting with liteorm — picking the query (explicit) vs orm (declarative) front-end, opening a backend, doing first CRUD, or wiring transactions.
---

# Using liteorm

liteorm is a CGo-free Go data library: two front-ends over one shared core. You open a backend once, get a `*liteorm.DB`, and pass it (or a transaction) as a `liteorm.Session` to whichever front-end you use. Backends import the driver; the core and front-ends do not.

## Choose a front-end

| Front-end | Import | Choose it when |
| --- | --- | --- |
| `query` | `liteorm.org/query` | You want explicit, typed SQL building: column predicates, joins, subqueries, unions, a CRUD `Repo`. Closest to SQL. |
| `orm` | `liteorm.org/orm` | You want declarative models: struct tags, AutoMigrate, associations, hooks, soft delete. Higher level. |

They share one core and one `Session`, so you can mix them on the same connection or transaction. Pick per task, not per project.

## Open a backend

```go
import (
    "liteorm.org/dialect/sqlite"
    "liteorm.org/dialect/postgres"
)

db, err := sqlite.Open("app.db")            // *liteorm.DB
defer db.Close()

db, err := postgres.Open(ctx, dsn)          // ctx-first; also mysql.Open, mssql.Open
```

| Backend | Open |
| --- | --- |
| SQLite | `sqlite.Open(path, ...opts)` — also `sqlite.OpenEncrypted(path, key)`, `sqlite.OpenConfig(cfg)` |
| Postgres | `postgres.Open(ctx, dsn, ...opts)` |
| MySQL | `mysql.Open(ctx, dsn, ...opts)` |
| SQL Server | `mssql.Open(ctx, dsn, ...opts)` |

## First CRUD

`query` front-end:

```go
type User struct {
    ID    int64
    Name  string
    Email string
}
func (User) TableName() string { return "users" }

repo := query.NewRepo[User](db)
u := User{Name: "Ada", Email: "ada@x.io"}
_ = repo.Insert(ctx, &u)          // PK read back into u via RETURNING
got, _ := repo.Get(ctx, u.ID)     // ErrNoRows if missing
u.Name = "Ada L."
_ = repo.Update(ctx, &u)
_ = repo.Delete(ctx, u.ID)
```

`orm` front-end (declarative — `Create`, soft-delete-aware reads, hooks):

```go
repo := orm.NewRepo[User](db)
u := User{Name: "Ada", Email: "ada@x.io"}
_ = repo.Create(ctx, &u)
got, _ := repo.Get(ctx, u.ID)
```

A model is just a struct with a `TableName() string` method. No method ⇒ the table name is `snake_case(TypeName)` with no pluralization.

## Session & transactions

`liteorm.Session` is the one interface both front-ends take. Both `*liteorm.DB` and a transaction satisfy it, so the same code runs on a connection or inside a tx.

```go
tx, err := db.Begin(ctx)          // *BoundTx, also a Session
defer tx.Rollback(ctx)           // no-op after Commit

_ = orm.NewRepo[User](tx).Create(ctx, &u)             // write via orm
rows, _ := query.Select[User](tx).Filter(...).All(ctx) // read via query — SAME tx

_ = tx.Commit(ctx)
```

Nested `tx.Begin(ctx)` opens a savepoint; `Rollback` undoes just that savepoint. You pick one front-end's `NewRepo`/`Select` per call but pass the same `tx` to both.

## Errors are normalized (errors.Is across backends)

```go
errors.Is(err, liteorm.ErrNoRows)          // also sql.ErrNoRows
errors.Is(err, liteorm.ErrUniqueViolation)
errors.Is(err, liteorm.ErrForeignKey)
errors.Is(err, liteorm.ErrNotNull)
errors.Is(err, liteorm.ErrCheck)
errors.Is(err, liteorm.ErrDeadlock)        // serialize/retry
errors.Is(err, liteorm.ErrSerialization)
```

## Deeper

- Guide: [../../docs/guides/query.md](../../docs/guides/query.md)
- API: https://pkg.go.dev/liteorm.org
