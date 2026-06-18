# Coming from gorm

LiteORM's `orm` front-end is deliberately close to gorm in spirit — declarative models, struct tags, associations, hooks, soft delete — so most of what you know transfers. It even **reads `gorm:"..."` tags natively**, so a model carried over works without rewriting a single tag. The differences are intentional: they trade a little magic for predictability (no lazy loading, no silent pluralization, errors instead of silent no-ops). This guide maps the API and calls out what changes.

## Verb mapping

| gorm | LiteORM |
| --- | --- |
| `db.Create(&u)` | `repo.Create(ctx, &u)` |
| `db.Save(&u)` | `repo.Save(ctx, &u)` |
| `db.First(&u, id)` / `Take` | `repo.Get(ctx, id)` (→ `ErrNoRows`) |
| `db.Find(&us)` | `repo.Find(ctx)` (scoped) · or `query.Select[T](db).All(ctx)` |
| `db.Where("age > ?", n)` | `.Where("age > ?", n)` (raw) · or `.Filter(query.Col[int]("age").Gt(n))` (typed) |
| `db.Pluck("email", &out)` | `query.Pluck(ctx, query.Select[User](db), query.Col[string]("email"))` |
| `db.Updates(values)` | `repo.Update(ctx, &v)` · partial: `repo.Updates(ctx, &v, "col", ...)` |
| `db.Delete(&u)` | `repo.Delete(ctx, &u)` (soft when the model has a `soft_delete` column) |
| `db.Unscoped().Delete(&u)` | `repo.ForceDelete(ctx, &u)` |
| `db.Unscoped().Find(&us)` | `repo.IncludeDeleted().Find(ctx)` |
| (un-delete by hand) | `repo.Restore(ctx, &u)` |
| `db.FirstOrCreate` / `FirstOrInit` | `repo.FirstOrCreate(ctx, &v, conds...)` / `repo.FirstOrInit(ctx, &v, conds...)` |
| `clause.OnConflict{...}` upsert | `repo.Upsert(ctx, &v, query.OnConflict("col").DoUpdate("a", "b"))` |
| `db.Preload("Posts")` | `orm.Load[User, Post](ctx, db, users, "Posts")` (one query, N+1-safe) |
| `db.Preload("A.B")` | `orm.LoadPath[User](ctx, db, users, "A.B")` · or `orm.NewPreloader[User](db).With("A.B").Load(ctx, users)` |
| `db.Model(&u).Association("Posts").Append(...)` | `orm.Assoc[User, Post](db, "Posts", &u)` → `Append`/`Replace`/`Delete`/`Clear`/`Count` |
| `db.AutoMigrate(&A{}, &B{})` | `orm.AutoMigrateAll(ctx, db, A{}, B{})` |
| `db.Scopes(fn)` | `repo.Scopes(fn)` |
| `db.Count(&n)` | `repo.Count(ctx)` |
| `db.Transaction(func(tx) { ... })` | `tx, _ := db.Begin(ctx); … ; tx.Commit(ctx)` (see below) |
| `db.Raw(sql).Scan(&out)` | `query.Raw[T](ctx, db, sql, args...)` |

## What's different (and why)

- **No table-name pluralization.** `User` maps to table `user`, not `users`. Add `func (User) TableName() string { return "users" }` to keep gorm's names. (Your `gorm:"..."` field tags need no change.)
- **`gorm:"..."` tags are read as-is.** A ported model behaves identically; you don't have to convert tags to `orm:"..."`. The one type change: a `gorm.DeletedAt` field becomes a `sql.NullTime` tagged `soft_delete` — the porter below does this for you.
- **Eager loading is explicit and N+1-safe.** There's no lazy loading; you call `orm.Load`/`LoadPath` for exactly the relations you want, and each is one batched query (the test suite asserts the query count). Touching an unloaded relation field gives the zero value, never a hidden query.
- **Keyed `Update`/`Delete` return `ErrNoRows` on no match.** Updating a missing or out-of-scope (soft-deleted) row is `liteorm.ErrNoRows`, not a silent success — so a no-op is something you can detect. (gorm reports `RowsAffected` instead.) Reach a soft-deleted row on purpose with `IncludeDeleted()`.
- **No cascade saves.** A write persists one model; you save associations explicitly (create the rows, then wire them with `orm.Assoc`). This keeps writes predictable and bulk-load-friendly.
- **Context first.** `ctx` is the first argument everywhere, instead of `db.WithContext(ctx)`.
- **Two front-ends.** For anything beyond CRUD — joins, unions, window functions, CTEs — drop to the explicit [`query`](query.md) builder on the *same* `Session`/transaction, rather than bending the ORM. A value fetched one way feeds the other.
- **Normalized errors.** Constraint and not-found errors map to portable sentinels (`liteorm.ErrUniqueViolation`, `ErrNoRows` — which *is* `sql.ErrNoRows`); test with `errors.Is` or the [`Is…` helpers](errors.md).

### Transactions

There's no closure-based `Transaction` helper; you drive the transaction directly, and a nested `Begin` is a savepoint:

```go
tx, err := db.Begin(ctx)
if err != nil {
	return err
}
defer tx.Rollback(ctx) // no-op after Commit

if err := orm.NewRepo[User](tx).Create(ctx, &u); err != nil {
	return err // deferred Rollback fires
}
return tx.Commit(ctx)
```

See [Transactions](transactions.md).

## The automated path

You don't have to rewrite tags by hand. The codegen porter rewrites `gorm:"..."` struct tags into native `orm:"..."` tags and fixes the `gorm.DeletedAt` field, so a gorm codebase can drop the `gorm.io/gorm` dependency and read as idiomatic LiteORM:

```go
import "liteorm.org/gen"

out, notes, err := gen.PortSource(src) // src is Go source bytes; out is the rewritten file
```

`notes` flags anything to handle by hand (an embedded `gorm.Model`, dropped unsupported keys). Because the `orm` package reads gorm tags natively, the port is about cleanliness and shedding the dependency — not correctness. See the runnable [`examples/gormport`](../../examples/gormport/) and the [codegen guide](codegen.md).

## See also

- [ORM models](orm.md) · [Associations](associations.md) · [Soft delete](soft-delete.md) — the declarative front-end in depth.
- [Cheat sheet](../reference/cheatsheet.md) — the whole surface at a glance.
- [Recipes](recipes.md) — common tasks, copy-paste.
