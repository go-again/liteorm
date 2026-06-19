---
name: query-builder
description: Use when writing explicit typed queries with liteorm's query front-end — predicates, joins, unions, subqueries/EXISTS, streaming, Raw, or the query.Repo CRUD.
---

# query builder

Import `liteorm.org/query`. Build a typed `SelectBuilder[T]` with `query.Select[T](sess)`, chain clauses, then call a terminal. Predicates are typed by value and validated against the model's columns at build time (an unknown column is an error, not silent SQL).

## Build a SELECT

```go
users, err := query.Select[User](sess).
    Filter(query.Col[string]("status").Eq("active")).
    OrderBy("created_at DESC").
    Limit(20).Offset(40).
    All(ctx)
```

| Clause | Method |
| --- | --- |
| Typed predicates (AND-joined) | `.Filter(preds...)` |
| Raw predicate (escape hatch) | `.Where("col > ?", v)` |
| Distinct | `.Distinct()` |
| Join (raw / typed) | `.Join(clause, args...)`, `.InnerJoin(table, on, args...)`, `.LeftJoin`, `.RightJoin`, `.CrossJoin(table)` |
| Group / having | `.GroupByCols(cols...)` (typed) or `.GroupBy(cols...)` (raw), `.Having("frag", args...)` |
| Override SELECT list | `.Project(cols...)` (raw) |
| Order / paginate | `.Order(query.Asc(col), query.Desc(col))` (typed) or `.OrderBy(terms...)` (raw), `.Limit(n)`, `.Offset(n)` |
| Compound | `.Union(other)`, `.UnionAll(other)` |

Typed `Order`/`GroupByCols` take column tokens (`query.Col[V]("x")` → `.Field()` for group; `query.Asc/Desc(col)` for order), validated against the model and dialect-quoted; the raw `OrderBy`/`GroupBy` string forms stay as the escape hatch and compose in call order.

Terminals: `.All(ctx) ([]T, error)`, `.First(ctx) (T, error)` (`ErrNoRows` if empty), `.Count(ctx) (int64, error)` (row count), `.Exists(ctx) (bool, error)`, `.Iter(ctx) iter.Seq2[T, error]`.

Typed aggregates (free functions over a builder): `query.Sum/Min/Max(ctx, b, col)` return the column's type, `query.Avg(ctx, b, col)` returns `float64`, `query.CountCol(ctx, b, col)` returns `int64` — whole-set, NULL → zero. Pluck one column to a slice: `query.Pluck(ctx, b, col)` → `[]V`. For a **grouped** aggregate, project into a result struct with `query.Into[T, R](ctx, b, fields...)` using `col.Field()` and `query.SumAs/AvgAs/MinAs/MaxAs/CountAs(col, alias)` (plus `query.Name`/`query.Expr` for plain/raw items).

```go
total, _ := query.Sum(ctx, query.Select[Order](sess).Filter(paid), query.Col[int64]("total"))
rows, _ := query.Into[Order, Stat](ctx,
    query.Select[Order](sess).GroupByCols(query.Col[int64]("customer_id").Field()),
    query.Col[int64]("customer_id").Field(), query.SumAs(query.Col[int64]("total"), "revenue"))
```

## Typed predicates

`query.Col[V]("name")` gives a `Column[V]`; its operators take values of type `V`.

```go
query.Col[string]("category").Eq("books")
query.Col[float64]("price").Gt(50)            // Eq Ne Gt Ge Lt Le
query.Col[string]("name").Like("%Pro%")             // raw pattern
query.Col[string]("name").HasPrefix("Pro")          // HasSuffix / Contains — % and _ escaped literally
query.Col[string]("category").In("books", "home")   // NotIn too
query.Col[int64]("deleted_at").IsNull()             // IsNotNull too
query.Col[int64]("post_id").EqCol(query.Col[int64]("id").Of("posts")) // column = column (correlation)
query.Match("body", "rocket")                       // SQLite MATCH (FTS5/spellfix/vec); rejected off SQLite
```

`HasPrefix`/`HasSuffix`/`Contains` escape `%`/`_` so user input matches literally (the safe substring search). `query.Match` is SQLite-only and feature-gated.

Combine with `query.And(...)`, `query.Or(...)`, `query.Not(p)`:

```go
query.And(
    query.Col[string]("category").Eq("electronics"),
    query.Or(query.Col[int64]("stock").Gt(0), query.Col[bool]("featured").Eq(true)),
)
```

## Joins

```go
query.Select[Product](sess).
    Distinct().
    InnerJoin("reviews", "reviews.product_id = products.id").
    Where("reviews.rating >= ?", 5).
    All(ctx)
```

The table identifier is dialect-quoted; the ON condition is raw SQL (it spans tables) and may carry `?` markers.

## Subqueries: IN and EXISTS

`InQuery` takes a subquery that must `Project` exactly one column:

```go
fiveStar := query.Select[Review](sess).Project("product_id").
    Filter(query.Col[int64]("rating").Ge(5))

query.Select[Product](sess).
    Filter(query.Col[int64]("id").InQuery(fiveStar)).   // NotInQuery too
    All(ctx)
```

`Exists` / `NotExists` wrap a (usually correlated) subquery — `Project("1")` and correlate via a raw `Where`:

```go
anyReview := query.Select[Review](sess).Project("1").
    Where("reviews.product_id = products.id")

query.Select[Product](sess).Filter(query.Exists(anyReview)).All(ctx)
```

Subquery columns are validated when placed in the predicate, so the error surfaces from the outer terminal before any SQL runs. Placeholders are renumbered correctly per dialect.

To *project* a correlated EXISTS as a boolean column (not filter on it), use `query.ExistsField(alias, sub)` in `Into`, correlating with `EqCol`. It renders a portable `CASE WHEN EXISTS (...) THEN 1 ELSE 0 END`, scanning into a `bool` on every backend:

```go
hasReview := query.ExistsField("has_review",
    query.Select[Review](sess).Filter(
        query.Col[int64]("product_id").EqCol(query.Col[int64]("id").Of("products"))))
rows, _ := query.Into[Product, row](ctx, query.Select[Product](sess),
    query.Name("id"), query.Name("name"), hasReview)
```

## Set operations, locking, DISTINCT ON

```go
cheap := query.Select[Product](sess).Filter(query.Col[float64]("price").Lt(50))
books := query.Select[Product](sess).Filter(query.Col[string]("category").Eq("books"))
combined, _ := cheap.Union(books).OrderBy("name").All(ctx)  // + UnionAll (keeps dups)
both, _ := cheap.Intersect(books).All(ctx)                  // + Except / IntersectAll / ExceptAll
```

Compound arms must share the same column shape; the receiver's `OrderBy`/`Limit` apply to the whole compound. `Intersect`/`Except` work on SQLite/Postgres/MSSQL (a clear build error on MySQL).

- **Row locking** (Postgres/MySQL, inside a tx): `.ForUpdate()` / `.ForShare()`, refined by `.SkipLocked()` or `.NoWait()`. The work-queue claim is `.OrderBy("id").Limit(1).ForUpdate().SkipLocked().First(ctx)`. A clear build error on SQLite/MSSQL.
- **`.DistinctOn(cols...)`** (Postgres): first row per distinct combination; pair with an `Order` whose leading terms match. A clear build error elsewhere.

Each of these gates honestly on `dialect.Feature`: an unsupported dialect fails loudly at build time, never emits surprising SQL.

## CTEs and subquery sources

A FROM source can be a table, a CTE, or a derived table:

```go
// CTE: With(name, sub) then From(name)
active := query.Select[User](sess).Filter(query.Col[bool]("active").Eq(true))
rows, _ := query.Select[User](sess).With("active", active).From("active").All(ctx)

// Recursive CTE — the recursive arm refers back to the CTE name via a raw Join:
anchor := query.Select[Cat](sess).Where("id = ?", root)
recurse := query.Select[Cat](sess).Join("JOIN subtree ON cats.parent_id = subtree.id")
tree, _ := query.Select[Cat](sess).WithRecursive("subtree", anchor.UnionAll(recurse)).From("subtree").All(ctx)

// Derived table (FROM subquery) / join-on-subquery:
big, _ := query.FromSubquery[Order](sess, "r", recent).Filter(query.Col[int64]("total").Gt(1000)).All(ctx)
joined, _ := query.Select[Customer](sess).JoinSub("INNER JOIN", "o", recent, "o.customer_id = customers.id").All(ctx)
```

`With`/`WithRecursive` are gated by `FeatCTE` (all backends); `JoinLateral` (subquery references earlier FROM items) is Postgres-only. The subquery's placeholders renumber into the outer statement automatically.

## Window functions & scalar subqueries (projection items → Into)

Window functions and per-row scalar subqueries are projection expressions; select them into a result struct with `query.Into`.

```go
// window: rank rows within a partition
query.Into[Sale, Ranked](ctx, query.Select[Sale](sess),
    query.Col[string]("region").Field(),
    query.RowNumber().Over(query.Over().
        PartitionBy(query.Col[string]("region").Field()).
        OrderBy(query.Desc(query.Col[int64]("amount"))), "rank"))

// scalar subquery in SELECT (beyond IN/EXISTS), correlated via a raw Where
sub := query.Select[Order](sess).Project("count(*)").Where("orders.user_id = users.id")
query.Into[User, WithCount](ctx, query.Select[User](sess),
    query.Col[string]("name").Field(), query.ScalarSubquery("orders", sub))
```

Window funcs: `RowNumber`/`Rank`/`DenseRank`, `Lag`/`Lead(col, n)`, running `WindowSum`/`WindowAvg`/`WindowCount`/`WindowMin`/`WindowMax(col)`, each finished with `.Over(window, alias)`. Build the window with `query.Over().PartitionBy(...Field).OrderBy(...OrderTerm)`. Scalar subquery binds renumber into the outer statement automatically.

## Stream with Iter (early-stop closes rows)

```go
for p, err := range query.Select[Product](sess).OrderBy("price").Iter(ctx) {
    if err != nil { return err }
    use(p)
    if done { break }   // breaking stops streaming and closes rows
}
```

## Raw escape hatch

`query.Raw[T]` maps rows into a struct T (use `db:"col"` tags for aliases):

```go
type catStat struct {
    Category string `db:"category"`
    Items    int64  `db:"items"`
}
stats, _ := query.Raw[catStat](ctx, sess,
    `SELECT category, count(*) AS items FROM products GROUP BY category`)
```

`Raw[T]` needs T to be a struct. For a single scalar, use `query.Pluck[T, V](ctx, b, col)` (a typed column) or `query.PluckExpr[T, V](ctx, b, "MAX(x)")` / `PluckExprFirst` (a raw expression) → `[]V` / `V`.

## Repo (CRUD)

```go
repo := query.NewRepo[T](sess)
```

| Method | Notes |
| --- | --- |
| `Insert(ctx, *v)` | Generated PK read back into v via RETURNING (or LastInsertId). |
| `InsertMany(ctx, vs)` | Bulk: pgx CopyFrom where available, else chunked multi-row VALUES. Does NOT read PKs back. |
| `Upsert(ctx, *v, query.OnConflict("col").DoUpdate("c2","c3"))` | DoUpdate optional (defaults to all non-conflict columns); `.DoNothing()` ignores the conflict (portable INSERT OR IGNORE). |
| `Find(ctx, preds...)` | All rows matching the typed predicates. |
| `Get(ctx, id)` | By primary key; `ErrNoRows` if missing. |
| `Update(ctx, *v)` | Writes non-key columns, keyed by PK. |
| `Delete(ctx, id)` | Hard delete by PK. |

```go
_ = repo.Upsert(ctx, &p, query.OnConflict("name").DoUpdate("stock", "price"))
_ = repo.Upsert(ctx, &seen, query.OnConflict("url").DoNothing()) // first writer wins; dups skipped
```

## Multi-row UPDATE / DELETE

The Repo writes one row by PK; `query.Update[T]` / `query.Delete[T]` write **many by condition**. `Set(col, v)` / `SetExpr(col, "raw", args...)` / `Inc(col, n)` / `Dec(col, n)`, `Where`/`Filter`, then `Exec(ctx) (int64 affected)` or `Returning(ctx) ([]T)` (the changed rows, via RETURNING/OUTPUT — errors on MySQL). A **WHERE-less write is refused** (use `Where("1 = 1")` to mean every row).

```go
n, _ := query.Update[Product](sess).Set("active", false).Filter(query.Col[int64]("stock").Eq(0)).Exec(ctx)
_, _ = query.Update[Product](sess).Inc("view_count", 1).Filter(query.Col[int64]("id").Eq(id)).Exec(ctx) // atomic col = col + 1
rows, _ := query.Update[Product](sess).SetExpr("stock", "stock + ?", 100).Where("category = ?", "x").Returning(ctx)
del, _ := query.Delete[Product](sess).Filter(query.Col[string]("category").Eq("legacy")).Exec(ctx)
```

`From(source)` adds a correlated `UPDATE … FROM` (set columns from another table / a `VALUES` list — the "set many rows to different values" pattern). Gated by `FeatUpdateFrom` (Postgres/SQLite/MSSQL; MySQL errors). Correlated DELETE: scope with a subquery predicate (`InQuery`/`Exists`).

## Pitfalls

- An unknown column in a `Filter` predicate is a build-time error, but a `Where("...")` raw fragment is not checked — typos in raw SQL fail at the DB.
- `query.Repo.Delete` and the JSONB/array operators differ from the orm front-end's soft delete — see the orm-models and pitfalls skills.
- `query.Raw[T]` expects a struct; for a single-column scalar use `query.Pluck` / `query.PluckExpr` instead (they scan into `[]V`).

## Deeper

- Guide: [../../docs/guides/query.md](../../docs/guides/query.md)
- API: https://pkg.go.dev/liteorm.org/query
