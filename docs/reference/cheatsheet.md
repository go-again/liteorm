# Cheat sheet

The whole surface on one screen. Each row links to the guide that explains it. For exact signatures, see [pkg.go.dev/liteorm.org](https://pkg.go.dev/liteorm.org).

## Open a backend

```go
db, err := sqlite.Open("app.db")                 // also OpenEncrypted(path, key), OpenConfig(cfg)
db, err := postgres.Open(ctx, dsn)               // native pgx
db, err := mysql.Open(ctx, dsn)
db, err := mssql.Open(ctx, dsn)
defer db.Close()
```

`*liteorm.DB` and a transaction (`db.Begin(ctx)`) are both a `liteorm.Session` — pass either to any front-end. See [Backends](backends.md).

## `orm.Repo[T]` — the declarative repository

`repo := orm.NewRepo[T](sess)`. Full method surface (see [ORM](../guides/orm.md), [soft delete](../guides/soft-delete.md)):

| Group | Methods |
| --- | --- |
| Create | `Create` · `CreateInBatches(vs, n)` |
| Insert-or-update | `Save` (by identity) · `Upsert(v, query.OnConflict(...).DoUpdate(...))` · `FirstOrCreate(v, conds...)` · `FirstOrInit(v, conds...)` (no write) |
| Read by key | `Get(keys...)` (→ `ErrNoRows`) · `GetByKeys(keys...)` (batch, single-PK) |
| Read (scoped) | `Find` · `First` · `Count` · `Exists` · `FindInBatches(n, fn)` |
| Read scopes (chain, return a view) | `Where` · `Filter(preds...)` · `OrderBy` · `Limit` · `Offset` · `Scopes(...)` · `IncludeDeleted` · `OnlyDeleted` |
| Update | `Update` · `Updates(v, cols...)` (partial) |
| Delete | `Delete` · `ForceDelete` · `Restore` (un-soft-delete) |
| Write scoping (return a view) | `Select(cols...)` · `Omit(cols...)` |

Keyed `Update`/`Delete`/`Restore` return `liteorm.ErrNoRows` when no row matches. `Create`/`Update`/`Delete` fire [hooks](../guides/hooks.md).

## Associations (orm)

`orm.Load[P, C](ctx, sess, parents, "Field", opts...)` — one batched query, N+1-safe; `opts`: `LoadWhere("...", args...)`, `LoadOrderBy("...")`. Nested: `orm.LoadPath[P](ctx, sess, roots, "A.B")` or `orm.NewPreloader[P](sess).With("A").With("B.C").Load(ctx, roots)`. Write the non-owner side with `orm.Assoc[P, C](sess, "Field", &owner)` → `Append`/`Delete`/`Replace`/`Clear`/`Count`; m2m primitives `orm.Attach`/`orm.Detach`. See [Associations](../guides/associations.md).

## Schema & migrations

```go
orm.AutoMigrate[T](ctx, sess, orm.WithForeignKeys())   // one model (opt-in FK constraints)
orm.AutoMigrateAll(ctx, sess, A{}, B{}, C{})           // a whole set, in dependency order
```

Reviewable changes: `orm.Diff[T]` → `Changes{Added, Removed, Changed}`; `orm.GenerateMigration[T]` → up/down SQL (executes nothing). Introspect: `orm.IntrospectColumns` / `IntrospectIndexes` / `IntrospectTables`. Runner — `migrate.New(sess)` → `Up`/`UpTo`/`Down`/`DownTo`/`Status`/`Version`/`Force`; `migrate.Load(fs.FS)`; `migrate.WritePair(dir, ver, name, up, down)`. See [Migrations](../guides/migrations.md).

## `query` — the explicit builder

`query.Select[T](sess)` then a terminal (see [Query builder](../guides/query.md)):

| Group | Surface |
| --- | --- |
| Terminals | `All` · `First` · `Count` · `Exists` · `Iter` (stream, `iter.Seq2`) |
| Filter / order / page | `Where(frag, args...)` · `Filter(preds...)` · `OrderBy` / `Order(Asc/Desc/OrderExpr)` · `Limit` · `Offset` · `Distinct` / `DistinctOn` |
| Joins | `InnerJoin` · `LeftJoin` · `RightJoin` · `CrossJoin` · `Join` · `JoinSub` · `JoinLateral` |
| Group | `GroupBy` / `GroupByCols` · `Having` |
| Set ops | `Union` / `UnionAll` · `Intersect`(`All`) · `Except`(`All`) |
| Locking | `ForUpdate` · `ForShare` · `SkipLocked` · `NoWait` |
| CTE / subquery source | `With` · `WithRecursive` · `From` · `query.FromSubquery` |
| Project / scalar | `Project` · `query.Pluck(ctx, b, col)` → `[]V` · `query.Into[T,R](ctx, b, fields...)` |
| Aggregates | `query.Sum/Avg/Min/Max/CountCol(ctx, b, col)` · `SumAs`/`AvgAs`/... in `Into` |
| Window / scalar subq | `RowNumber`/`Rank`/`DenseRank`/`Lag`/`Lead`/`WindowSum`...`.Over(...)` · `ScalarSubquery` |

Predicates: `query.Col[V]("col").Eq/Ne/Gt/Ge/Lt/Le/In/NotIn/Like/IsNull/IsNotNull` · `query.And/Or/Not` · `Exists`/`NotExists` · Postgres `query.JSON(...)` / `query.Array[E](...)`.

CRUD: `query.NewRepo[T](sess)` → `Insert` · `InsertMany` (bulk) · `Get` · `Find(preds...)` · `Update` · `Delete` · `Upsert`. Multi-row writes: `query.Update[T](sess).Set/SetExpr(...).Where/Filter(...).Exec(ctx)` (returns affected count) and `query.Delete[T](sess)...Exec`. Escape hatch: `query.Raw[T](ctx, sess, sql, args...)`.

## Errors

```go
liteorm.ErrNoRows · ErrUniqueViolation · ErrForeignKey · ErrNotNull · ErrCheck · ErrDeadlock · ErrSerialization
```

Test with `errors.Is(err, …)` or the helpers: `liteorm.IsUniqueViolation` · `IsForeignKeyViolation` · `IsNotNullViolation` · `IsCheckViolation` · `IsNotFound` · `IsRetryable` (deadlock/serialization). See [Errors](../guides/errors.md).

## See also

- [Getting started](../getting-started.md) · [Recipes](../guides/recipes.md) (common tasks) · [Coming from GORM](../guides/from-gorm.md)
- [Backends](backends.md) · [Dialects](dialects.md)
