# Soft delete

A soft delete marks a row as deleted instead of removing it, so the data stays recoverable and your history stays intact. In LiteORM you opt a model in with one field, and the [orm repository](orm.md) handles the rest: deletes become updates, reads exclude deleted rows by default, and explicit scopes let you see them when you need to.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/orm](https://pkg.go.dev/liteorm.org/orm).

## Opting in

Add a timestamp field tagged `soft_delete`. The conventional type is `sql.NullTime` — null while the row is live, set to the deletion time once it's soft-deleted:

```go
import "database/sql"

type Post struct {
	ID        int64
	Title     string
	Slug      string       `orm:"slug,unique"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
}

func (Post) TableName() string { return "posts" }
```

That single tag changes the behavior of `Delete` and of every read on the model's repository.

## Deleting and the default scope

`Delete` on a soft-delete model issues an `UPDATE` that stamps the timestamp column — it does not remove the row. By default, reads then exclude that row: `Find`, `Get`, and the repository's other reads only see rows whose delete timestamp is null.

```go
posts := orm.NewRepo[Post](db)

posts.Delete(ctx, &p) // sets deleted_at = now; the row stays in the table

live, _ := posts.Find(ctx)      // excludes p
_, err := posts.Get(ctx, p.ID)  // → liteorm.ErrNoRows
```

## Tri-state scopes

Reads run under one of three scopes. The default excludes deleted rows; two repository views opt into the others. Each returns a new repository view and leaves the original untouched, so you scope per query rather than mutating shared state.

| I want… | Use |
| --- | --- |
| only live rows (the default) | `posts` |
| live and deleted rows together | `posts.IncludeDeleted()` |
| deleted rows only | `posts.OnlyDeleted()` |

```go
live, _ := posts.Find(ctx)                   // live only
withDel, _ := posts.IncludeDeleted().Find(ctx) // live + deleted
onlyDel, _ := posts.OnlyDeleted().Find(ctx)    // deleted only
```

Naming the scope explicitly — rather than burying it in a flag or a double negative — keeps the intent of each read obvious at the call site.

## Permanently removing a row: ForceDelete

When you genuinely want the row gone, `ForceDelete` issues a hard `DELETE` even on a soft-delete model. Use it to purge a row that's already soft-deleted, or to bypass the soft-delete behavior outright:

```go
posts.Delete(ctx, &p)      // soft delete: sets deleted_at
posts.ForceDelete(ctx, &p) // hard delete: removes the row for good
```

Both paths fire the `BeforeDelete` / `AfterDelete` [hooks](hooks.md).

## Bringing a row back: Restore

`Restore` is the symmetric partner to `Delete` — it clears the `deleted_at` timestamp, returning a soft-deleted row to the live set. It reaches the row by primary key regardless of the delete scope (so it works on an already-deleted row), fires the `BeforeUpdate` / `AfterUpdate` hooks, and returns `liteorm.ErrNoRows` if no row matches:

```go
posts.Delete(ctx, &p)  // soft delete
posts.Restore(ctx, &p) // un-delete: deleted_at back to NULL, row live again
```

## Unique columns are freed on soft delete

A soft-deleted row would normally keep occupying any `unique` column — so you couldn't reuse, say, a slug or an email belonging to a deleted record. LiteORM avoids this: `AutoMigrate` builds the unique constraint as a *partial* unique index scoped to live rows (`... WHERE deleted_at IS NULL` on SQLite, Postgres, and MSSQL; a functional unique index on MySQL, which lacks partial indexes). The effect is the same everywhere — once a row is soft-deleted, its unique value is released and a new live row can take it.

```go
posts.Delete(ctx, &p) // p had slug "iterators-are-nice"

// The slug is free again — this Create succeeds.
reuse := Post{Title: "Iterators are nice", Slug: "iterators-are-nice"}
err := posts.Create(ctx, &reuse)
```

This behavior comes from how `AutoMigrate` emits the index, so models you migrate with it get the fix automatically. See [migrations](migrations.md) for more on how schema is created and kept in sync.

## Where to next

- [The orm front-end](orm.md) — the repository and its CRUD.
- [Hooks](hooks.md) — `BeforeDelete` / `AfterDelete` fire on soft and hard deletes.
- [Migrations](migrations.md) — how the partial unique index is created.
