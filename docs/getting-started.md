# Getting started

LiteORM gives you two ways to talk to a database, sharing one core. The **`query`** front-end is explicit and generics-first — you write the shape of the query and get typed rows back. The **`orm`** front-end is declarative — you describe a model with struct tags and get CRUD, associations, hooks, soft-delete, and migrations. They interoperate: both take a `liteorm.Session` (a database handle or a transaction), so a value fetched one way feeds the other on the same transaction.

> **Using an AI coding assistant?** LiteORM ships [Agent Skills](guides/ai-agents.md) that give your assistant the exact, current API for each task, so it writes correct LiteORM the first time. Setting them up is the highest-leverage few minutes here — see [AI agents & skills](guides/ai-agents.md).

## Install

Add the core plus the backend you want. The core (`liteorm.org`) pulls in **zero** database drivers; each backend is its own module, so your dependency graph only carries the driver you actually use.

```
go get liteorm.org
go get liteorm.org/dialect/sqlite     # CGo-free SQLite (via gosqlite.org)
# or: liteorm.org/dialect/postgres  ·  liteorm.org/dialect/mysql  ·  liteorm.org/dialect/mssql
```

## Open a database

A backend's `Open` returns a `*liteorm.DB`, which is a `liteorm.Session` — the handle both front-ends accept.

```go
import "liteorm.org/dialect/sqlite"

db, err := sqlite.Open("app.db") // WAL + sensible pragmas applied
if err != nil {
	log.Fatal(err)
}
defer db.Close()
```

Other backends open from a DSN (Postgres uses the native pgx protocol):

```go
import "liteorm.org/dialect/postgres"

db, err := postgres.Open(ctx, "postgres://user:pw@localhost/app?sslmode=disable")
```

See [Backends](reference/backends.md) for every DSN shape.

## Your first query

The `query` front-end builds a typed `SELECT` over a model type. A model is any struct; a `TableName()` method (or the snake_case of the type name) names the table.

```go
import "liteorm.org/query"

type Product struct {
	ID       int64
	Name     string
	Category string
	Price    float64
}

func (Product) TableName() string { return "products" }

// Typed predicates, validated against the model's columns at build time.
hits, err := query.Select[Product](db).
	Filter(query.And(
		query.Col[string]("category").Eq("books"),
		query.Col[float64]("price").Lt(40),
	)).
	OrderBy("price").
	All(ctx)
```

CRUD lives on a `Repo`:

```go
repo := query.NewRepo[Product](db)
p := Product{Name: "Go in Practice", Category: "books", Price: 39.50}
_ = repo.Insert(ctx, &p)
got, err := repo.Get(ctx, p.ID) // returns liteorm.ErrNoRows if absent
```

More — joins, unions, subqueries, streaming, upserts, bulk insert — in the [query guide](guides/query.md).

## Your first model

The `orm` front-end is declarative. Tag the struct, migrate it, and use the Repo.

```go
import "liteorm.org/orm"

type Author struct {
	ID    int64
	Name  string
	Email string `orm:"email,unique"`
}

func (Author) TableName() string { return "authors" }

_ = orm.AutoMigrate[Author](ctx, db) // additive: creates the table + indexes

authors := orm.NewRepo[Author](db)
ada := Author{Name: "Ada", Email: "ada@example.com"}
_ = authors.Create(ctx, &ada)
```

Most apps have several models. Migrate them all in one call, in dependency order (referenced tables first) — the same additive guarantees apply:

```go
_ = orm.AutoMigrateAll(ctx, db, Author{}, Book{}, Review{})
```

From here: [associations](guides/associations.md) (with N+1-safe eager loading), [hooks](guides/hooks.md), [soft delete](guides/soft-delete.md), and [migrations](guides/migrations.md).

## Transactions and interop

`db.Begin(ctx)` returns a transaction that is also a `Session`. Pass it to either front-end; a nested `Begin` is a savepoint.

```go
tx, _ := db.Begin(ctx)
_ = orm.NewRepo[Author](tx).Create(ctx, &Author{Name: "Grace", Email: "grace@example.com"})
back, _ := query.Select[Author](tx).Filter(query.Col[string]("name").Eq("Grace")).First(ctx)
_ = tx.Commit(ctx)
```

Details in [Transactions](guides/transactions.md).

## Errors are normalized

Constraint and not-found errors map to the same sentinels on every backend, so you write the check once:

```go
if errors.Is(err, liteorm.ErrUniqueViolation) { /* ... */ }
if errors.Is(err, liteorm.ErrNoRows)          { /* ... */ }
```

See [Errors](guides/errors.md).

## Where to go next

- The [query](guides/query.md) and [orm](guides/orm.md) guides cover each front-end in depth.
- [Code generation](guides/codegen.md) adds compile-time column safety and SQL→Go.
- Backend-specific power: [SQLite search](guides/sqlite-search.md), [SQLite changesets](guides/sqlite-changeset.md), [Postgres LISTEN/NOTIFY + JSONB](guides/postgres.md).
- Runnable programs live under [`examples/`](../examples/); `just example <name>` runs one, `just examples` runs them all.
- [AI agents & skills](guides/ai-agents.md) — point your AI assistant at the shipped skills so it writes correct LiteORM without guessing.
