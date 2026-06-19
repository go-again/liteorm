# The query front-end

The `query` package is LiteORM's explicit, generics-first query builder. You write `query.Select[T](sess)`, chain typed predicates and clauses, and finish with a terminal that runs the SQL and scans rows straight into your struct `T`. The same code runs against a `*liteorm.DB` or a transaction, because both satisfy the `liteorm.Session` interface — see [transactions](transactions.md).

If you prefer a declarative, model-driven style with associations, hooks, and migrations, see the [orm front-end](orm.md). The two share one core and interoperate on a single transaction, so picking one here does not lock you out of the other.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/query](https://pkg.go.dev/liteorm.org/query).

## Opening a database

Every query needs a session. Open one with a dialect package; SQLite is the simplest:

```go
import (
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

db, err := sqlite.Open("app.db")
if err != nil {
	return err
}
defer db.Close()
```

Other backends open the same way and return the same `*liteorm.DB`, so nothing below changes when you switch: `postgres.Open(ctx, dsn)`, `mysql.Open(ctx, dsn)`, `mssql.Open(ctx, dsn)`. See [getting started](../getting-started.md) for the full setup.

## Your model type

A model is a plain struct. The table name comes from a `TableName() string` method if you define one, otherwise it's the snake_case of the type name — singular by default, or pluralized if you opt in with `orm.UsePluralTableNames(true)` (both front-ends share that one setting).

```go
type Product struct {
	ID       int64
	Name     string
	Category string
	Price    float64
	Stock    int64
	Active   bool
}

func (Product) TableName() string { return "products" }
```

## Selecting rows

`query.Select[T](sess)` starts a typed `SELECT` over `T`. Call `.All(ctx)` to get every matching row as `[]T`:

```go
products, err := query.Select[Product](db).All(ctx)
```

## Typed predicates

The heart of the builder is `Filter`, which takes one or more typed, column-validated predicates. Build a predicate from a typed column token — `query.Col[V]("name")` — and an operator. The value type `V` is checked at compile time, and the column name is validated against your model's schema when the query runs (an unknown column is a clear error, never silent SQL).

```go
hot, err := query.Select[Product](db).
	Filter(
		query.Col[string]("category").Eq("electronics"),
		query.Col[float64]("price").Gt(50),
	).
	OrderBy("price DESC").
	All(ctx)
```

Multiple predicates passed to `Filter` (or stacked across several `Filter` calls) are joined with `AND`.

### The operators

| I want… | Predicate |
| --- | --- |
| equality / inequality | `.Eq(v)` `.Ne(v)` |
| ordering comparisons | `.Gt(v)` `.Ge(v)` `.Lt(v)` `.Le(v)` |
| pattern match (raw pattern) | `.Like("%pro%")` |
| literal substring (wildcards escaped) | `.HasPrefix("foo")` `.HasSuffix(".go")` `.Contains("ab")` |
| set membership | `.In(a, b, c)` `.NotIn(a, b)` |
| NULL tests | `.IsNull()` `.IsNotNull()` |
| column vs column | `.EqCol(query.Col[V]("x").Of("t"))` |
| IN a subquery | `.InQuery(sub)` `.NotInQuery(sub)` |

`HasPrefix`/`HasSuffix`/`Contains` escape any `%`/`_` in the needle so it matches literally — the safe way to do a prefix/suffix/contains search on user input, where `.Like` would treat those characters as wildcards. On SQLite, `query.Match(col, q)` adds the `MATCH` operator for FTS5 / spellfix1 / sqlite-vec virtual tables (see [SQLite search](sqlite-search.md)); it composes in `Filter` like any predicate but is rejected at build time on other dialects.

### Combining predicates: And / Or / Not

`Filter`'s top-level `AND` covers the common case. For anything richer, compose explicitly with `query.And`, `query.Or`, and `query.Not`, which nest to any depth:

```go
mixed, err := query.Select[Product](db).
	Filter(query.Or(
		query.Col[string]("category").In("books", "home"),
		query.Col[string]("name").Like("%Pro%"),
	)).
	OrderBy("name").
	All(ctx)
```

`query.Not(p)` negates a single predicate.

## Ordering and pagination

`Order` takes typed terms — `query.Asc(col)` / `query.Desc(col)` — each validated against the model and quoted by the dialect. `OrderBy` is the raw-string escape hatch (so you control collation, functions, `NULLS FIRST`); the two compose in call order. `Limit` and `Offset` page the result.

```go
page, err := query.Select[Product](db).
	Filter(query.Col[bool]("active").Eq(true)).
	Order(query.Desc(query.Col[float64]("price")), query.Asc(query.Col[string]("name"))).
	Limit(20).
	Offset(40).
	All(ctx)

// raw escape hatch (e.g. a collation or a NULLS ordering the typed form can't express):
recent, _ := query.Select[Product](db).OrderBy("created_at DESC NULLS LAST").All(ctx)
```

## Single row, count, existence

Three terminals beyond `.All`:

- `.First(ctx)` returns the first matching row, or `liteorm.ErrNoRows` if there are none. It applies `LIMIT 1` for you.
- `.Count(ctx)` returns the matching row count as `int64`, ignoring order/limit/offset.
- `.Exists(ctx)` returns a `bool`.

```go
cheapest, err := query.Select[Product](db).
	Filter(query.Col[int64]("stock").Gt(0)).
	OrderBy("price").
	First(ctx)
if errors.Is(err, liteorm.ErrNoRows) {
	// nothing in stock
}

inStock, _ := query.Select[Product](db).Filter(query.Col[int64]("stock").Gt(0)).Count(ctx)
anyInactive, _ := query.Select[Product](db).Filter(query.Col[bool]("active").Eq(false)).Exists(ctx)
```

## Streaming with Iter

When a result set is large and you'd rather not hold it all in memory, use `.Iter(ctx)`. It returns an `iter.Seq2[T, error]` you range over directly; rows are scanned lazily and the underlying rows are closed when you stop — including an early `break`.

```go
n := 0
for p, err := range query.Select[Product](db).OrderBy("price").Iter(ctx) {
	if err != nil {
		return err
	}
	fmt.Println(p.Name, p.Price)
	if n++; n == 3 {
		break // streaming stops early; rows are closed
	}
}
```

## Distinct, GroupBy, Having

`Distinct()` adds `SELECT DISTINCT`. `GroupByCols(cols...)` groups by typed, validated, dialect-quoted columns (`GroupBy(cols...)` is the raw-string escape hatch); `Having(frag, args...)` adds a raw, AND-joined HAVING condition (the `frag` carries positional `?` markers bound by `args`).

```go
rows, err := query.Select[Product](db).
	Distinct().
	GroupByCols(query.Col[string]("category").Field()).
	Having("count(*) > ?", 2).
	All(ctx)
```

### Aggregates

For a **whole-set** aggregate, the typed terminals build `SELECT AGG(col)` over your filters and return the scalar (a result over no rows comes back as the zero value, not an error):

```go
revenue, _ := query.Sum(ctx, query.Select[Order](db).Filter(paid), query.Col[int64]("total"))
avgPrice, _ := query.Avg(ctx, query.Select[Product](db), query.Col[float64]("price")) // returns float64
cheapest, _ := query.Min(ctx, query.Select[Product](db), query.Col[float64]("price"))
```

`Sum`/`Min`/`Max` return the column's type, `Avg` returns `float64`, and `CountCol` returns `int64`. (`.Count(ctx)` remains the row-count terminal.)

To pull a **single column** into a slice — the "give me all the emails / ids" read — use `Pluck`, which projects one typed column and scans the values directly (no full-row structs):

```go
emails, _ := query.Pluck(ctx, query.Select[User](db).Filter(active), query.Col[string]("email"))
// emails is []string, honoring the builder's filters/order/limit
```

For a **grouped** aggregate, project the grouped columns and the aggregate expressions into a result struct with `Into` — column-validated and dialect-quoted, the typed counterpart of `Raw`:

```go
type byCategory struct {
	Category string `db:"category"`
	Revenue  int64  `db:"revenue"`
}
stats, err := query.Into[Product, byCategory](ctx,
	query.Select[Product](db).GroupByCols(query.Col[string]("category").Field()),
	query.Col[string]("category").Field(),
	query.SumAs(query.Col[float64]("price"), "revenue"))
```

The aggregate projection helpers are `SumAs`/`AvgAs`/`MinAs`/`MaxAs`/`CountAs(col, alias)`; `Name("col")` projects a plain column and `Expr("…")` is the raw escape hatch within a projection. The result struct's columns must match the projection's names and aliases. For a fully custom shape, `Raw` (below) stays available.

## Joins

For a join keyed by a column you control, use the typed helpers. The table identifier is quoted by the dialect; the `ON` condition is raw SQL (it spans tables) and may carry `?` markers:

```go
top, err := query.Select[Product](db).
	Distinct().
	InnerJoin("reviews", "reviews.product_id = products.id").
	Where("reviews.rating >= ?", 5).
	OrderBy("products.id").
	All(ctx)
```

The full set: `InnerJoin(table, on, args...)`, `LeftJoin`, `RightJoin`, `CrossJoin(table)`, and a fully raw `Join(clause, args...)` escape hatch when you want to write the whole join clause yourself.

`Where(frag, args...)` seen above is the raw, AND-joined predicate escape hatch — reach for it when a condition spans joined tables or needs SQL that the typed predicates don't cover; prefer `Filter` for conditions on your own model's columns.

## Projecting columns

By default the full model column set is selected. `Project(cols...)` overrides the SELECT list with raw column expressions — most often to select a single column for an IN-subquery, or to pull specific columns or aggregates. For a custom result shape, `Into[T, R]` projects typed `Field`s (`Name`, `Column.Field`, the `AggAs` helpers, and `ExistsField` below) into a result struct `R`.

To pull a single scalar into a slice, `Pluck(b, col)` returns one typed column as `[]V`; `PluckExpr[T, V](b, expr, args...)` does the same for a raw scalar expression — `MAX(x)`, `COALESCE(a, b)`, `LENGTH(t)`, `rowid` — and `PluckExprFirst` returns just the first value.

## Subqueries: IN and EXISTS

Build a subquery like any other `Select`, then drop it into a predicate.

For an IN-subquery, the inner query must `Project` exactly one column. Its columns are validated when it's placed in the predicate, so an error surfaces from the outer query's terminal before any SQL runs:

```go
fiveStar := query.Select[Review](db).
	Project("product_id").
	Filter(query.Col[int64]("rating").Ge(5))

viaSub, err := query.Select[Product](db).
	Filter(query.Col[int64]("id").InQuery(fiveStar)).
	OrderBy("id").
	All(ctx)
```

For an EXISTS / NOT EXISTS predicate, use `query.Exists(sub)` / `query.NotExists(sub)`. The subquery typically correlates to the outer query through a raw `Where`:

```go
anyReview := query.Select[Review](db).
	Project("1").
	Where("reviews.product_id = products.id")

reviewed, err := query.Select[Product](db).
	Filter(query.Exists(anyReview)).
	OrderBy("id").
	All(ctx)
```

To *project* whether a correlated subquery matches as a boolean result column (rather than filter on it), use `query.ExistsField(alias, sub)` in `Into`. Correlate the subquery to the outer row with the typed `EqCol` (`query.Col[V]("inner").EqCol(query.Col[V]("outer").Of("table"))`) instead of a raw `Where`. It renders a portable `CASE WHEN EXISTS (...) THEN 1 ELSE 0 END`, so it scans into a `bool` on every backend:

```go
hasReview := query.ExistsField("has_review",
	query.Select[Review](db).Filter(
		query.Col[int64]("product_id").EqCol(query.Col[int64]("id").Of("products"))))

type row struct {
	ID        int64
	Name      string
	HasReview bool
}
rows, err := query.Into[Product, row](ctx, query.Select[Product](db).OrderBy("id"),
	query.Name("id"), query.Name("name"), hasReview)
```

## Set operations

Combine two compatible selects (same column shape). `Union` removes duplicate rows; `UnionAll` keeps them. The receiver's `ORDER BY` / `LIMIT` apply to the whole compound.

```go
cheapElectronics := query.Select[Product](db).Filter(query.And(
	query.Col[string]("category").Eq("electronics"),
	query.Col[float64]("price").Lt(50),
))
allBooks := query.Select[Product](db).Filter(query.Col[string]("category").Eq("books"))

combined, err := cheapElectronics.Union(allBooks).OrderBy("name").All(ctx)
```

`Intersect` (rows in both) and `Except` (rows in the first but not the second) round out the set operators, each with an `…All` variant that keeps duplicates:

```go
both, _ := a.Intersect(b).All(ctx)
only, _ := a.Except(b).All(ctx)
```

`INTERSECT` / `EXCEPT` are supported on SQLite, Postgres, and SQL Server; on MySQL they raise a clear build error (MySQL only added them in 8.0.31, so LiteORM doesn't advertise them there).

## Row locking

On Postgres and MySQL, a `SELECT` can take row locks. `ForUpdate()` takes exclusive locks, `ForShare()` shared ones; `SkipLocked()` skips already-locked rows instead of blocking, and `NoWait()` errors instead. Take locks inside a transaction.

```go
tx, _ := db.Begin(ctx)
job, err := query.Select[Job](tx).
	Filter(query.Col[string]("status").Eq("queued")).
	OrderBy("id").Limit(1).
	ForUpdate().SkipLocked().      // the classic work-queue claim
	First(ctx)
// … process job, update status, tx.Commit(ctx)
```

Locking is gated by dialect: SQLite (no row locks) and SQL Server (which uses table hints instead) raise a clear build error rather than emit SQL that wouldn't mean what you intended.

## DISTINCT ON (Postgres)

`Distinct()` adds plain `SELECT DISTINCT`. On Postgres, `DistinctOn(cols...)` keeps the first row of each distinct combination of the given columns — pair it with an `Order` whose leading terms match to choose *which* row:

```go
// the latest event per kind
latest, err := query.Select[Event](db).
	DistinctOn(query.Col[string]("kind").Field()).
	Order(query.Asc(query.Col[string]("kind")), query.Desc(query.Col[int64]("seq"))).
	All(ctx)
```

`DistinctOn` raises a clear build error on the dialects that don't support it.

## CTEs and subquery sources

A FROM source can be a base table, a **common table expression**, or a **derived table** (subquery). `With(name, sub)` prepends a CTE; reference it with `From(name)`:

```go
active := query.Select[User](db).Filter(query.Col[bool]("active").Eq(true))
rows, err := query.Select[User](db).
	With("active_users", active).
	From("active_users").
	Filter(query.Col[int64]("age").Gt(18)).
	All(ctx)
```

`WithRecursive(name, sub)` builds a recursive CTE — the recursive arm refers back to the CTE name (via a raw `Join`), and the two arms are combined with `UnionAll`. This is how you walk a tree or graph in one query:

```go
anchor := query.Select[Category](db).Where("id = ?", rootID)
recurse := query.Select[Category](db).Join("JOIN subtree ON categories.parent_id = subtree.id")
subtree, err := query.Select[Category](db).
	WithRecursive("subtree", anchor.UnionAll(recurse)).
	From("subtree").
	All(ctx) // the root and all its descendants
```

`FromSubquery[T](sess, alias, sub)` selects from a **derived table**, and `JoinSub(kind, alias, sub, on)` joins one — the subquery's placeholders renumber into the outer statement automatically:

```go
recent := query.Select[Order](db).Where("created_at > ?", cutoff)
big, err := query.FromSubquery[Order](db, "r", recent).
	Filter(query.Col[int64]("total").Gt(1000)).
	All(ctx)

withOrders, err := query.Select[Customer](db).
	JoinSub("INNER JOIN", "o", recent, "o.customer_id = customers.id").
	All(ctx)
```

CTEs are gated by `FeatCTE` (every backend supports them); `JoinLateral` (a `LATERAL` join, where the subquery may reference earlier `FROM` items) is Postgres-only and raises a clear build error elsewhere.

## Window functions and scalar subqueries

Window functions and per-row scalar subqueries are *projection expressions* — you select them into a result struct with [`Into`](#aggregates). A window function is built from a function (`RowNumber`/`Rank`/`DenseRank`, `Lag`/`Lead`, or a running `WindowSum`/`WindowAvg`/`WindowCount`/`WindowMin`/`WindowMax`), an `Over(...)` spec (`PartitionBy` + `OrderBy`), and a result alias:

```go
type Ranked struct {
	Region string
	Amount int64
	Rank   int64 `db:"rank"`
}
ranked, err := query.Into[Sale, Ranked](ctx,
	query.Select[Sale](db),
	query.Col[string]("region").Field(),
	query.Col[int64]("amount").Field(),
	query.RowNumber().Over(
		query.Over().
			PartitionBy(query.Col[string]("region").Field()).
			OrderBy(query.Desc(query.Col[int64]("amount"))),
		"rank"))
```

`ScalarSubquery(alias, sub)` puts a subquery in the SELECT list as a single per-row value — the typed answer to a computed column beyond `IN`/`EXISTS`. The subquery must select one column and yield at most one row; correlate it with a raw `Where` referencing the outer table, and its bind parameters renumber into the outer statement automatically:

```go
type WithCount struct {
	Name       string
	OpenOrders int64 `db:"open_orders"`
}
openByUser := query.Select[Order](db).Project("count(*)").
	Where("orders.user_id = users.id AND orders.status = ?", "open")
rows, err := query.Into[User, WithCount](ctx,
	query.Select[User](db),
	query.Col[string]("name").Field(),
	query.ScalarSubquery("open_orders", openByUser))
```

Window functions need a modern engine (SQLite 3.25+, Postgres, MySQL 8+, SQL Server). For anything more exotic, raw `Project` plus `Raw[T]` remains the escape hatch.

## The Repo: CRUD

`query.NewRepo[T](sess)` is a typed repository wrapping the common write paths and primary-key lookups. It requires a primary key on `T` for the keyed operations.

```go
repo := query.NewRepo[Product](db)

// Insert one; the generated primary key is read back into v in place.
p := Product{Name: "Desk Lamp", Category: "home", Price: 24, Stock: 18, Active: true}
err := repo.Insert(ctx, &p)
fmt.Println(p.ID) // populated

// Lookup by primary key (→ liteorm.ErrNoRows when absent).
got, err := repo.Get(ctx, p.ID)

// Find by predicates (same predicates as the builder).
cheapBooks, err := repo.Find(ctx,
	query.Col[string]("category").Eq("books"),
	query.Col[float64]("price").Lt(40),
)

// Update non-key columns of the row identified by its primary key.
p.Price = 19.99
err = repo.Update(ctx, &p)

// Delete by primary key.
err = repo.Delete(ctx, p.ID)
```

### Bulk insert

`InsertMany(ctx, vs)` inserts a slice efficiently — using the backend's native bulk path when available (Postgres `CopyFrom`), otherwise chunked multi-row `VALUES`. It does not read primary keys back, so use `Insert` per row when you need each generated id.

```go
err := repo.InsertMany(ctx, []Product{
	{Name: "Laptop Pro", Category: "electronics", Price: 1899, Stock: 7, Active: true},
	{Name: "USB Cable", Category: "electronics", Price: 9.99, Stock: 230, Active: true},
})
```

### Upsert

`Upsert(ctx, v, query.OnConflict("col"))` inserts `v` or, on a conflict with the named columns, updates the row. By default every non-conflict column is overwritten; chain `.DoUpdate(...)` to overwrite only specific columns:

```go
restock := Product{Name: "USB Cable", Category: "electronics", Price: 8.49, Stock: 500, Active: true}
err := repo.Upsert(ctx, &restock, query.OnConflict("name").DoUpdate("stock", "price"))
```

To *ignore* a conflicting row instead of updating it — the typed form of `INSERT OR IGNORE`, and the canonical SQL `ON CONFLICT DO NOTHING` — chain `.DoNothing()`. It is portable: a no-op `ON DUPLICATE KEY UPDATE` on MySQL, a `MERGE` with no matched arm on SQL Server.

```go
err := repo.Upsert(ctx, &seen, query.OnConflict("url").DoNothing()) // first writer wins; dups skipped
```

## Multi-row UPDATE and DELETE

The `Repo` writes one row by primary key; `query.Update[T]` and `query.Delete[T]` are the builders for writing *many* rows by condition. `Set`/`SetExpr` assign columns, `Where`/`Filter` scope the statement, and `Exec` returns the number of rows affected. A WHERE-less write is **refused** (add `Where("1 = 1")` to affect every row on purpose).

```go
deactivated, err := query.Update[Product](db).
	Set("active", false).
	Filter(query.Col[int64]("stock").Eq(0)).
	Exec(ctx) // rows affected

discontinued, err := query.Delete[Product](db).
	Filter(query.Col[string]("category").Eq("legacy")).
	Exec(ctx)
```

`Returning(ctx)` runs the write and scans the changed rows back as `[]T` — via `RETURNING` (Postgres/SQLite) or `OUTPUT` (SQL Server); it errors on MySQL, which has neither:

```go
restocked, err := query.Update[Product](db).
	SetExpr("stock", "stock + ?", 100). // a raw expression, not just a value
	Filter(query.Col[string]("category").Eq("electronics")).
	Returning(ctx) // []Product, the updated rows
```

For the common atomic read-modify-write, `Inc`/`Dec` are typed sugar over `SetExpr` — the increment happens in the database, with the column quoted for the dialect:

```go
_, err := query.Update[Product](db).
	Inc("view_count", 1). // view_count = view_count + 1, atomically
	Filter(query.Col[int64]("id").Eq(id)).
	Exec(ctx)
```

`From(source)` adds a correlated `UPDATE … FROM` — set columns from another table (or a `VALUES` list), which is also how you set many rows to *different* values in one statement. Gated by `FeatUpdateFrom` (Postgres / SQLite / SQL Server; MySQL, which uses `UPDATE … JOIN`, raises a clear build error):

```go
// age += adjustments.delta, joined per row
_, err := query.Update[Person](db).
	SetExpr("age", "age + adj.delta").
	From("adjustments AS adj").
	Where("people.id = adj.person_id").
	Exec(ctx)
```

For a correlated DELETE, scope it with a subquery predicate (`InQuery` / `Exists`) — portable across every dialect.

## The Raw escape hatch

When you need SQL the builder doesn't express — window functions, CTEs, hand-tuned aggregates — drop to `query.Raw[T]`. It runs your SQL with bound args and scans the rows into any result type `T` (often a small struct shaped to the projection, with `db:"..."` tags):

```go
type catStat struct {
	Category string `db:"category"`
	Items    int64  `db:"items"`
	Total    int64  `db:"total"`
}

stats, err := query.Raw[catStat](ctx, db,
	`SELECT category, count(*) AS items, sum(stock) AS total
	 FROM products GROUP BY category ORDER BY total DESC`)
```

`Raw` is the intended path for complex SQL such as window functions and CTEs.

## Postgres JSON and array predicates

On Postgres, the builder adds typed predicates for JSON/JSONB columns and array columns. JSON path extraction works through `query.JSON("col").Key("k")...` with `Eq`/`Ne`/`Like`/`In` and the JSONB containment `Contains`; array columns use `query.Array[E]("col")` with `Contains`/`ContainedBy`/`Overlaps`/`Has`. These operators are Postgres-only and fail loudly at build time on a dialect that doesn't support them.

```go
admins, err := query.Select[Account](db).
	Filter(query.JSON("profile").Key("role").Eq("admin")).
	All(ctx)

tagged, err := query.Select[Article](db).
	Filter(query.Array[string]("tags").Contains("go", "databases")).
	All(ctx)
```

See [Postgres](postgres.md) for the full treatment of these operators.

## Where to next

- [The orm front-end](orm.md) — declarative models, migrations, associations.
- [Transactions](transactions.md) — running these same builders inside a tx.
- [Index](../index.md) and [getting started](../getting-started.md).
