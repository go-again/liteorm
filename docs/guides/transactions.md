# Transactions

A transaction in LiteORM is a `*liteorm.BoundTx` you get from `db.Begin(ctx)`. It satisfies the same `liteorm.Session` interface as `*liteorm.DB`, so every builder, repository, and helper in both front-ends works on it unchanged — you just pass the transaction where you'd otherwise pass the database.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org](https://pkg.go.dev/liteorm.org).

## Begin, commit, rollback

`db.Begin(ctx)` starts a transaction. Commit it with `.Commit(ctx)` to persist your work, or `.Rollback(ctx)` to discard it. A deferred rollback is the safe idiom — it's a no-op once you've committed.

```go
tx, err := db.Begin(ctx)
if err != nil {
	return err
}
defer tx.Rollback(ctx) // harmless after a successful Commit

repo := query.NewRepo[Account](tx)
if err := repo.Insert(ctx, &from); err != nil {
	return err
}
if err := repo.Insert(ctx, &to); err != nil {
	return err
}

return tx.Commit(ctx)
```

Because the transaction is a `Session`, both front-ends run inside it the same way they run on the database:

```go
// query builder, inside the tx
hot, err := query.Select[Product](tx).
	Filter(query.Col[bool]("active").Eq(true)).
	All(ctx)

// orm repository, inside the tx
posts := orm.NewRepo[Post](tx)
err = posts.Create(ctx, &p)
```

## Nested transactions are savepoints

Calling `.Begin(ctx)` on a transaction opens a nested transaction implemented as a SAVEPOINT. Rolling back the nested handle undoes only the work done since that savepoint; the outer transaction keeps everything before it. This lets you make part of a unit of work optional without abandoning the whole thing.

```go
tx, err := db.Begin(ctx)
if err != nil {
	return err
}

query.NewRepo[Product](tx).Insert(ctx, &Product{Name: "Keeper"})

sp, err := tx.Begin(ctx) // nested = savepoint
if err != nil {
	return err
}
query.NewRepo[Product](sp).Insert(ctx, &Product{Name: "Doomed"})
sp.Rollback(ctx) // undo just the savepoint — "Keeper" survives

tx.Commit(ctx)
```

After this runs, `Keeper` is persisted and `Doomed` is not.

## query and orm interoperate on one transaction

The two front-ends share LiteORM's core, so a value written through one is immediately visible to the other on the same transaction — no flush, no second connection, no surprises. Write declaratively with `orm` and read with the explicit `query` builder (or the reverse) inside a single atomic unit:

```go
tx, err := db.Begin(ctx)
if err != nil {
	return err
}
defer tx.Rollback(ctx)

// Write via the orm repository.
hopper := Author{Name: "Hopper", Email: "hopper@x.io"}
if err := orm.NewRepo[Author](tx).Create(ctx, &hopper); err != nil {
	return err
}

// Read it back via the query builder, on the same tx, before commit.
back, err := query.Select[Author](tx).
	Filter(query.Col[int64]("id").Eq(hopper.ID)).
	First(ctx)
if err != nil {
	return err
}
fmt.Println(back.Name) // "Hopper"

return tx.Commit(ctx)
```

## Errors during a transaction

The normalized error values work the same inside a transaction as outside it — check them with `errors.Is` and decide whether to roll back:

```go
if err := repo.Insert(ctx, &dup); err != nil {
	if errors.Is(err, liteorm.ErrUniqueViolation) {
		// handle the conflict; the deferred Rollback will discard the tx
		return err
	}
	return err
}
```

The normalized set spans `liteorm.ErrNoRows`, `ErrUniqueViolation`, `ErrForeignKey`, `ErrNotNull`, `ErrCheck`, `ErrDeadlock`, and `ErrSerialization`, consistent across every backend — so retry-on-`ErrSerialization` logic you write for one database works on all of them.

## Where to next

- [The query front-end](query.md) — the builder and repository you run inside a tx.
- [The orm front-end](orm.md) — declarative models, equally transaction-aware.
- [Hooks](hooks.md) — lifecycle callbacks fire inside the transaction that triggered them.
