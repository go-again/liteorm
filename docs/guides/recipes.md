# Recipes

Copy-paste solutions to the tasks that come up most. Each links to the guide that explains it in depth. Assume `ctx` and an opened `db` are in scope.

## Paginate

Keyset ("cursor") pagination — stable and offset-free, the right choice for APIs:

```go
id := query.Col[int64]("id")
page, _ := query.Select[User](db).
	Filter(id.Gt(cursor)).   // cursor = last id seen (0 for the first page)
	Order(query.Asc(id)).
	Limit(pageSize).All(ctx)
// next cursor = page[len(page)-1].ID
```

Classic offset pagination (portable to MSSQL's `OFFSET…FETCH`):

```go
page, _ := query.Select[User](db).Order(query.Asc(id)).Limit(pageSize).Offset(n).All(ctx)
```

## Insert many rows

```go
users := []*User{{Name: "a"}, {Name: "b"}, {Name: "c"}}
_ = orm.NewRepo[User](db).CreateInBatches(ctx, users, 500) // one multi-row INSERT per chunk
// query side: query.NewRepo[User](db).InsertMany(ctx, users) (uses pgx CopyFrom when available)
```

## Upsert (insert or update on conflict)

```go
_ = orm.NewRepo[Language](db).Upsert(ctx, &Language{Code: "en", Name: "English"},
	query.OnConflict("code").DoUpdate("name")) // narrow DoUpdate cols to preserve e.g. created_at
```

## Fetch many rows by primary key

```go
users, _ := orm.NewRepo[User](db).GetByKeys(ctx, 1, 2, 3) // one WHERE id IN (...) query
```

## Process a large table without loading it all

Stream row-by-row with `Iter` (a Go 1.23 range-over-func):

```go
for u, err := range query.Select[User](db).Where("active = ?", true).Iter(ctx) {
	if err != nil { return err }
	// handle u
}
```

Or process in fixed-size chunks (keyset under the hood):

```go
err := orm.NewRepo[User](db).Where("active = ?", true).
	FindInBatches(ctx, 1000, func(batch []User) error {
		// handle batch
		return nil
	})
```

## Atomically increment a column

Compute the new value in SQL — no read-modify-write race:

```go
_, err := query.Update[Post](db).SetExpr("views", "views + ?", 1).Where("id = ?", id).Exec(ctx)
```

## Eager-load only some related rows

Filter and order the batched children query (still one query, N+1-safe):

```go
orm.Load[Author, Post](ctx, db, authors, "Posts",
	orm.LoadWhere("published_at IS NOT NULL"),
	orm.LoadOrderBy("published_at DESC"))
```

## Pull a single column into a slice

```go
emails, _ := query.Pluck(ctx, query.Select[User](db).Filter(active), query.Col[string]("email")) // []string
```

## Soft delete and restore

```go
posts := orm.NewRepo[Post](db)
posts.Delete(ctx, &p)              // soft delete (sets deleted_at); excluded from reads
posts.Restore(ctx, &p)             // un-delete: deleted_at back to NULL
posts.ForceDelete(ctx, &p)         // hard delete, even on a soft-delete model
gone, _ := posts.OnlyDeleted().Find(ctx) // the deleted rows
```

## Retry a transaction on a transient failure

`IsRetryable` is true for a deadlock or serialization failure — roll back and try again:

```go
for attempt := 0; attempt < 3; attempt++ {
	tx, err := db.Begin(ctx)
	if err != nil { return err }
	err = doWork(ctx, tx) // your repo/query calls on tx
	if err == nil {
		if err = tx.Commit(ctx); err == nil { return nil }
	}
	_ = tx.Rollback(ctx)
	if !liteorm.IsRetryable(err) { return err }
}
return errors.New("gave up after retries")
```

## Map errors at an HTTP boundary

```go
switch {
case liteorm.IsNotFound(err):        return c.JSON(404, ...)
case liteorm.IsUniqueViolation(err): return c.JSON(409, ...) // duplicate
case liteorm.IsForeignKeyViolation(err): return c.JSON(409, ...)
case err != nil:                     return c.JSON(500, ...)
}
```

## See also

- [Cheat sheet](../reference/cheatsheet.md) — the whole surface at a glance.
- Deep guides: [query](query.md) · [ORM](orm.md) · [associations](associations.md) · [soft delete](soft-delete.md) · [errors](errors.md) · [transactions](transactions.md).
