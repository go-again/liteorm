package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

type Sale struct {
	ID     int64
	Region string
	Amount int64
}

func (Sale) TableName() string { return "sales" }

// TestWindowAndScalarSubqueryLive exercises the Phase-5 projection expressions —
// window functions and a scalar subquery in the SELECT list — against a live
// database, including placeholder renumbering across the projection and the WHERE.
func TestWindowAndScalarSubqueryLive(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "win.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE sales (id INTEGER PRIMARY KEY, region TEXT NOT NULL, amount INTEGER NOT NULL)`,
		`INSERT INTO sales (id,region,amount) VALUES (1,'west',100),(2,'west',300),(3,'east',200),(4,'east',50)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("ROW_NUMBER partitioned ranking", func(t *testing.T) {
		type ranked struct {
			Region string
			Amount int64
			Rn     int64 `db:"rn"`
		}
		got, err := query.Into[Sale, ranked](ctx,
			query.Select[Sale](db).Order(query.Asc(query.Col[string]("region")), query.Desc(query.Col[int64]("amount"))),
			query.Col[string]("region").Field(),
			query.Col[int64]("amount").Field(),
			query.RowNumber().Over(
				query.Over().
					PartitionBy(query.Col[string]("region").Field()).
					OrderBy(query.Desc(query.Col[int64]("amount"))),
				"rn"))
		if err != nil {
			t.Fatal(err)
		}
		// Highest amount per region ranks #1.
		top := map[string]int64{} // region -> amount where rn==1
		for _, r := range got {
			if r.Rn == 1 {
				top[r.Region] = r.Amount
			}
		}
		if top["west"] != 300 || top["east"] != 200 {
			t.Errorf("rn==1 amounts = %v, want west:300 east:200", top)
		}
	})

	t.Run("scalar subquery in SELECT, binds renumber across projection and WHERE", func(t *testing.T) {
		type row struct {
			Amount  int64
			Over100 int64 `db:"over100"`
		}
		// Projection carries a bind (> ?, 100); the outer query carries another
		// (< ?, 250). Correct renumbering binds 100 to the subquery, 250 to WHERE.
		over100 := query.Select[Sale](db).Project("count(*)").Where("amount > ?", 100)
		got, err := query.Into[Sale, row](ctx,
			query.Select[Sale](db).Where("amount < ?", 250).Order(query.Asc(query.Col[int64]("amount"))),
			query.Col[int64]("amount").Field(),
			query.ScalarSubquery("over100", over100))
		if err != nil {
			t.Fatal(err)
		}
		// rows with amount<250: 50, 100, 200 → 3; over100 (300,200) == 2 for each.
		if len(got) != 3 {
			t.Fatalf("rows = %d, want 3 (%+v)", len(got), got)
		}
		for _, r := range got {
			if r.Over100 != 2 {
				t.Errorf("over100 = %d for amount %d, want 2 (placeholder renumbering wrong?)", r.Over100, r.Amount)
			}
		}
	})
}
