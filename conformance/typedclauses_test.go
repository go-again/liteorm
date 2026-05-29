package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

// TestTypedClausesLive exercises the Phase-1 typed clause builder (Order, typed
// GroupBy, scalar aggregates, and the Into projection) against a live database —
// proving they execute, not just render. Models Person/Order2 come from
// complexquery_test.go.
func TestTypedClausesLive(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tc.db"))
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

	t.Run("typed Order (Asc/Desc)", func(t *testing.T) {
		got, err := query.Select[Person](db).Order(query.Desc(query.Col[int64]("age"))).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{3, 4, 2, 1} // oldest first: Cy 70, Di 55, Bo 40, Ada 12
		for i, p := range got {
			if p.ID != want[i] {
				t.Fatalf("Order(Desc age)) ids = %v, want %v", idsOf(got), want)
			}
		}
	})

	t.Run("scalar aggregates over orders", func(t *testing.T) {
		ob := func() *query.SelectBuilder[Order2] { return query.Select[Order2](db) }
		total := query.Col[int64]("total")
		if sum, _ := query.Sum(ctx, ob(), total); sum != 490 {
			t.Errorf("Sum = %d, want 490", sum)
		}
		if avg, _ := query.Avg(ctx, ob(), total); avg < 163.0 || avg > 164.0 {
			t.Errorf("Avg = %v, want ~163.33", avg)
		}
		if mn, _ := query.Min(ctx, ob(), total); mn != 40 {
			t.Errorf("Min = %d, want 40", mn)
		}
		if mx, _ := query.Max(ctx, ob(), total); mx != 300 {
			t.Errorf("Max = %d, want 300", mx)
		}
		if n, _ := query.CountCol(ctx, ob(), total); n != 3 {
			t.Errorf("CountCol = %d, want 3", n)
		}
		// An aggregate over no rows returns the zero value (NULL → 0), not an error.
		empty, err := query.Sum(ctx, query.Select[Order2](db).Filter(total.Gt(99999)), total)
		if err != nil || empty != 0 {
			t.Errorf("Sum(empty) = %d err=%v, want 0/nil", empty, err)
		}
	})

	t.Run("grouped aggregate via Into", func(t *testing.T) {
		type personTotal struct {
			PersonID int64 `db:"person_id"`
			Revenue  int64 `db:"revenue"`
		}
		rows, err := query.Into[Order2, personTotal](ctx,
			query.Select[Order2](db).
				GroupByCols(query.Col[int64]("person_id").Field()).
				Order(query.Desc(query.Col[int64]("person_id"))),
			query.Col[int64]("person_id").Field(),
			query.SumAs(query.Col[int64]("total"), "revenue"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].PersonID != 4 || rows[0].Revenue != 300 ||
			rows[1].PersonID != 2 || rows[1].Revenue != 190 {
			t.Errorf("grouped revenue = %+v, want [{4 300} {2 190}]", rows)
		}
	})
}

func idsOf(ps []Person) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}
