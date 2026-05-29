package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

type Person struct {
	ID   int64
	Name string
	Age  int64
}

func (Person) TableName() string { return "people" }

// TestComplexQueriesLive exercises the join / union / subquery builder additions
// against a live SQLite database — proving they execute, not just render.
func TestComplexQueriesLive(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "cq.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, person_id INTEGER NOT NULL, total INTEGER NOT NULL)`,
		`INSERT INTO people (id,name,age) VALUES (1,'Ada',12),(2,'Bo',40),(3,'Cy',70),(4,'Di',55)`,
		`INSERT INTO orders (id,person_id,total) VALUES (1,2,150),(2,2,40),(3,4,300)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(ps []Person) []int64 {
		out := make([]int64, len(ps))
		for i, p := range ps {
			out[i] = p.ID
		}
		return out
	}
	eq := func(label string, got, want []int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", label, got, want)
			}
		}
	}

	t.Run("inner join filters to people with a big order", func(t *testing.T) {
		got, err := query.Select[Person](db).
			Distinct().
			InnerJoin("orders", "orders.person_id = people.id").
			Where("orders.total >= ?", 100).
			OrderBy("people.id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		eq("join", ids(got), []int64{2, 4})
	})

	t.Run("union of the young and the old", func(t *testing.T) {
		young := query.Select[Person](db).Filter(query.Col[int64]("age").Lt(18))
		old := query.Select[Person](db).Filter(query.Col[int64]("age").Gt(65))
		got, err := young.Union(old).OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		eq("union", ids(got), []int64{1, 3})
	})

	t.Run("IN subquery: people with an order over 100", func(t *testing.T) {
		sub := query.Select[Order2](db).Project("person_id").Filter(query.Col[int64]("total").Gt(100))
		got, err := query.Select[Person](db).
			Filter(query.Col[int64]("id").InQuery(sub)).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		eq("in-subquery", ids(got), []int64{2, 4})
	})

	t.Run("EXISTS subquery: people who ordered anything", func(t *testing.T) {
		sub := query.Select[Order2](db).Project("1").Where("orders.person_id = people.id")
		got, err := query.Select[Person](db).
			Filter(query.Exists(sub)).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		eq("exists", ids(got), []int64{2, 4})
	})
}

type Order2 struct {
	ID       int64
	PersonID int64
	Total    int64
}

func (Order2) TableName() string { return "orders" }
