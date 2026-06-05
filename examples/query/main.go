// Command query is a comprehensive tour of liteorm's explicit `query` front-end
// on SQLite: typed column predicates, ordering/pagination, First/Count/Exists,
// iter.Seq2 streaming, raw aggregates, Repo CRUD + bulk insert + upsert, nested
// transactions with savepoints, and normalized errors. It runs against a
// throwaway database and cleans up after itself.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

type Product struct {
	ID       int64
	Name     string
	Category string
	Price    float64
	Stock    int64
	Active   bool
	AddedAt  time.Time
}

func (Product) TableName() string { return "products" }

// Review is a second table, used to show joins and subqueries.
type Review struct {
	ID        int64
	ProductID int64
	Rating    int64
}

func (Review) TableName() string { return "reviews" }

const schema = `CREATE TABLE products (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	name     TEXT NOT NULL UNIQUE,
	category TEXT NOT NULL,
	price    REAL NOT NULL,
	stock    INTEGER NOT NULL,
	active   INTEGER NOT NULL,
	added_at TIMESTAMP NOT NULL
)`

const reviewsSchema = `CREATE TABLE reviews (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	product_id INTEGER NOT NULL,
	rating     INTEGER NOT NULL
)`

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-query-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, reviewsSchema); err != nil {
		return err
	}
	repo := query.NewRepo[Product](db)

	// ---- Bulk insert (CopyFrom where the backend supports it, else multi-row) ----
	section("InsertMany (bulk)")
	now := time.Now().UTC()
	catalog := []Product{
		{Name: "Laptop Pro", Category: "electronics", Price: 1899.00, Stock: 7, Active: true, AddedAt: now},
		{Name: "Phone X", Category: "electronics", Price: 999.00, Stock: 0, Active: true, AddedAt: now},
		{Name: "USB Cable", Category: "electronics", Price: 9.99, Stock: 230, Active: true, AddedAt: now},
		{Name: "Go in Practice", Category: "books", Price: 39.50, Stock: 12, Active: true, AddedAt: now},
		{Name: "SQL Antipatterns", Category: "books", Price: 34.95, Stock: 3, Active: false, AddedAt: now},
		{Name: "Desk Lamp", Category: "home", Price: 24.00, Stock: 18, Active: true, AddedAt: now},
	}
	if err := repo.InsertMany(ctx, catalog); err != nil {
		return err
	}
	fmt.Printf("inserted %d products\n", len(catalog))

	// ---- Typed, column-validated predicates: And + comparisons ----
	section("Filter: active electronics over $50, priced high→low")
	hot, err := query.Select[Product](db).
		Filter(query.And(
			query.Col[string]("category").Eq("electronics"),
			query.Col[float64]("price").Gt(50),
			query.Col[bool]("active").Eq(true),
		)).
		OrderBy("price DESC").
		All(ctx)
	if err != nil {
		return err
	}
	for _, p := range hot {
		fmt.Printf("  %-16s $%.2f\n", p.Name, p.Price)
	}

	// ---- Or / In / Like ----
	section("Filter: books|home, OR name LIKE '%Pro%'")
	mixed, err := query.Select[Product](db).
		Filter(query.Or(
			query.Col[string]("category").In("books", "home"),
			query.Col[string]("name").Like("%Pro%"),
		)).
		OrderBy("name").
		All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  %s\n", names(mixed))

	// ---- First (single row → ErrNoRows) ----
	section("First: cheapest in-stock product")
	cheapest, err := query.Select[Product](db).
		Filter(query.Col[int64]("stock").Gt(0)).
		OrderBy("price").
		First(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  %s ($%.2f, %d in stock)\n", cheapest.Name, cheapest.Price, cheapest.Stock)

	// ---- Count + Exists ----
	section("Count / Exists")
	inStock, _ := query.Select[Product](db).Filter(query.Col[int64]("stock").Gt(0)).Count(ctx)
	anyInactive, _ := query.Select[Product](db).Filter(query.Col[bool]("active").Eq(false)).Exists(ctx)
	fmt.Printf("  %d products in stock; any inactive? %v\n", inStock, anyInactive)

	// ---- Typed aggregates (whole-set) ----
	section("Typed aggregates")
	priceCol := query.Col[float64]("price")
	avgPrice, _ := query.Avg(ctx, query.Select[Product](db), priceCol)
	maxPrice, _ := query.Max(ctx, query.Select[Product](db), priceCol)
	totalStock, _ := query.Sum(ctx, query.Select[Product](db), query.Col[int64]("stock"))
	fmt.Printf("  avg price $%.2f, max price $%.2f, total stock %d\n", avgPrice, maxPrice, totalStock)

	// ---- Typed grouped aggregate via GroupByCols + Into ----
	section("Typed grouped aggregate: stock by category")
	type catStat struct {
		Category string `db:"category"`
		Items    int64  `db:"items"`
		Total    int64  `db:"total"`
	}
	catCol := query.Col[string]("category")
	stats, err := query.Into[Product, catStat](ctx,
		query.Select[Product](db).
			GroupByCols(catCol.Field()).
			Order(query.Asc(catCol)), // order by the grouped column
		catCol.Field(),
		query.CountAs(query.Col[int64]("id"), "items"),
		query.SumAs(query.Col[int64]("stock"), "total"))
	if err != nil {
		return err
	}
	for _, s := range stats {
		fmt.Printf("  %-12s %d items, %d in stock\n", s.Category, s.Items, s.Total)
	}

	// ---- Streaming with iter.Seq2 (early stop) ----
	section("Iter: stream products by price, stop after 3")
	n := 0
	for p, err := range query.Select[Product](db).OrderBy("price").Iter(ctx) {
		if err != nil {
			return err
		}
		fmt.Printf("  %s ($%.2f)\n", p.Name, p.Price)
		if n++; n == 3 {
			break // streaming stops early; rows are closed
		}
	}

	// ---- Repo CRUD: Get, Find, Update, Upsert ----
	section("Repo: Get / Find / Update / Upsert")
	lamp, _ := repo.Get(ctx, cheapestID(db, ctx, "Desk Lamp"))
	fmt.Printf("  Get: %s\n", lamp.Name)
	cheapBooks, _ := repo.Find(ctx, query.Col[string]("category").Eq("books"), query.Col[float64]("price").Lt(40))
	fmt.Printf("  Find(books < $40): %s\n", names(cheapBooks))
	lamp.Price = 19.99
	_ = repo.Update(ctx, &lamp)
	reread, _ := repo.Get(ctx, lamp.ID)
	fmt.Printf("  Update: Desk Lamp now $%.2f\n", reread.Price)
	// Upsert on the unique name: restock instead of erroring.
	restock := Product{Name: "USB Cable", Category: "electronics", Price: 8.49, Stock: 500, Active: true, AddedAt: now}
	_ = repo.Upsert(ctx, &restock, query.OnConflict("name").DoUpdate("stock", "price"))
	cable, _ := repo.Get(ctx, restock.ID)
	fmt.Printf("  Upsert: USB Cable restocked to %d @ $%.2f\n", cable.Stock, cable.Price)

	// ---- Multi-row UPDATE / DELETE builders (by condition, not by PK) ----
	section("Multi-row UPDATE / DELETE")
	bumped, err := query.Update[Product](db).
		SetExpr("stock", "stock + ?", 10). // a raw expression
		Filter(query.Col[string]("category").Eq("books")).
		Returning(ctx) // the updated rows, via RETURNING
	if err != nil {
		return err
	}
	fmt.Printf("  bumped %d book(s) stock by 10: %s\n", len(bumped), names(bumped))
	removed, err := query.Delete[Product](db).Filter(query.Col[bool]("active").Eq(false)).Exec(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  deleted %d inactive product(s)\n", removed)

	// Products auto-increment to ids 1..6 in insertion order; seed some reviews.
	_ = query.NewRepo[Review](db).InsertMany(ctx, []Review{
		{ProductID: 1, Rating: 5}, // Laptop Pro
		{ProductID: 1, Rating: 4},
		{ProductID: 2, Rating: 3}, // Phone X
		{ProductID: 4, Rating: 5}, // Go in Practice
	})

	// ---- Join: products that have a 5★ review ----
	section("InnerJoin + Where (distinct)")
	top, err := query.Select[Product](db).
		Distinct().
		InnerJoin("reviews", "reviews.product_id = products.id").
		Where("reviews.rating >= ?", 5).
		OrderBy("products.id").All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  5★ products: %s\n", names(top))

	// ---- IN subquery: the same set, expressed as a subquery ----
	section("IN (subquery)")
	fiveStar := query.Select[Review](db).Project("product_id").Filter(query.Col[int64]("rating").Ge(5))
	viaSub, err := query.Select[Product](db).
		Filter(query.Col[int64]("id").InQuery(fiveStar)).
		OrderBy("id").All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  id IN (SELECT product_id …): %s\n", names(viaSub))

	// ---- EXISTS: products with any review at all ----
	section("EXISTS (correlated subquery)")
	anyReview := query.Select[Review](db).Project("1").Where("reviews.product_id = products.id")
	reviewed, err := query.Select[Product](db).
		Filter(query.Exists(anyReview)).
		OrderBy("id").All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  reviewed products: %s\n", names(reviewed))

	// ---- UNION: combine two filtered selects ----
	section("Set operations (Union / Except)")
	cheapElectronics := func() *query.SelectBuilder[Product] {
		return query.Select[Product](db).Filter(query.And(
			query.Col[string]("category").Eq("electronics"),
			query.Col[float64]("price").Lt(50),
		))
	}
	allBooks := func() *query.SelectBuilder[Product] {
		return query.Select[Product](db).Filter(query.Col[string]("category").Eq("books"))
	}
	combined, err := cheapElectronics().Union(allBooks()).OrderBy("name").All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  cheap electronics ∪ books: %s\n", names(combined))
	// EXCEPT: in-stock products that aren't books (INTERSECT/EXCEPT, SQLite-supported).
	stocked := query.Select[Product](db).Filter(query.Col[int64]("stock").Gt(0))
	notBooks, err := stocked.Except(allBooks()).OrderBy("name").All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  in stock, minus books: %s\n", names(notBooks))

	// ---- CTE + derived-table (subquery) FROM ----
	section("CTE + derived table")
	expensive := query.Select[Product](db).Filter(query.Col[float64]("price").Ge(100))
	pricey, err := query.Select[Product](db).
		With("pricey", expensive). // WITH "pricey" AS (...)
		From("pricey").            // SELECT ... FROM "pricey"
		Order(query.Desc(query.Col[float64]("price"))).
		All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  pricey (via CTE): %s\n", names(pricey))
	inStockSub := query.Select[Product](db).Filter(query.Col[int64]("stock").Gt(0))
	cheapInStock, err := query.FromSubquery[Product](db, "s", inStockSub).
		Filter(query.Col[float64]("price").Lt(40)).
		Order(query.Asc(query.Col[string]("name"))).
		All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  cheap & in stock (derived table): %s\n", names(cheapInStock))

	// ---- Window function: rank products by price within each category ----
	section("Window function (rank by price within category)")
	type ranked struct {
		Name     string
		Category string
		Rank     int64 `db:"rank"`
	}
	ranks, err := query.Into[Product, ranked](ctx,
		query.Select[Product](db).Order(query.Asc(query.Col[string]("category")), query.Desc(query.Col[float64]("price"))),
		query.Col[string]("name").Field(),
		query.Col[string]("category").Field(),
		query.RowNumber().Over(
			query.Over().
				PartitionBy(query.Col[string]("category").Field()).
				OrderBy(query.Desc(query.Col[float64]("price"))),
			"rank"))
	if err != nil {
		return err
	}
	for _, r := range ranks {
		fmt.Printf("  %-12s #%d in %s\n", r.Name, r.Rank, r.Category)
	}

	// ---- Transaction with a nested savepoint that rolls back ----
	section("Transaction + savepoint rollback")
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	_ = query.NewRepo[Product](tx).Insert(ctx, &Product{Name: "Keeper", Category: "home", Price: 5, Stock: 1, Active: true, AddedAt: now})
	sp, err := tx.Begin(ctx) // nested = savepoint
	if err != nil {
		return err
	}
	_ = query.NewRepo[Product](sp).Insert(ctx, &Product{Name: "Doomed", Category: "home", Price: 5, Stock: 1, Active: true, AddedAt: now})
	_ = sp.Rollback(ctx) // undo just the savepoint
	_ = tx.Commit(ctx)
	got, _ := query.Select[Product](db).Filter(query.Col[string]("name").In("Keeper", "Doomed")).All(ctx)
	fmt.Printf("  persisted after savepoint rollback: %s\n", names(got))

	// ---- Normalized errors ----
	section("Normalized errors")
	dupErr := repo.Insert(ctx, &Product{Name: "Laptop Pro", Category: "electronics", Price: 1, Stock: 1, Active: true, AddedAt: now})
	fmt.Printf("  duplicate insert → errors.Is(ErrUniqueViolation) = %v\n", errors.Is(dupErr, liteorm.ErrUniqueViolation))
	_, missErr := repo.Get(ctx, 999999)
	fmt.Printf("  Get(missing) → errors.Is(ErrNoRows) = %v\n", errors.Is(missErr, liteorm.ErrNoRows))

	fmt.Println()
	return nil
}

func names(ps []Product) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return "[" + strings.Join(out, ", ") + "]"
}

func cheapestID(db *liteorm.DB, ctx context.Context, name string) int64 {
	p, _ := query.Select[Product](db).Filter(query.Col[string]("name").Eq(name)).First(ctx)
	return p.ID
}
