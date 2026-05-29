package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

// TestWriteBuildersLive exercises the Phase-4 write builders against a live
// database: WHERE-based Update/Delete, RETURNING into typed rows, and a correlated
// UPDATE … FROM (which doubles as the set-many-rows-to-different-values pattern).
// Models Person/Order2 come from complexquery_test.go.
func TestWriteBuildersLive(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE adjustments (person_id INTEGER NOT NULL, delta INTEGER NOT NULL)`,
		`INSERT INTO people (id,name,age) VALUES (1,'Ada',12),(2,'Bo',40),(3,'Cy',70),(4,'Di',55)`,
		`INSERT INTO adjustments (person_id,delta) VALUES (2,10),(4,-5)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Update Exec returns rows affected", func(t *testing.T) {
		n, err := query.Update[Person](db).
			Set("age", 0).
			Filter(query.Col[int64]("age").Lt(18)).
			Exec(ctx) // only Ada (12)
		if err != nil || n != 1 {
			t.Fatalf("Update Exec = %d err=%v, want 1", n, err)
		}
	})

	t.Run("Update Returning yields the changed rows", func(t *testing.T) {
		got, err := query.Update[Person](db).
			SetExpr("age", "age + ?", 1).
			Where("name = ?", "Bo").
			Returning(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Name != "Bo" || got[0].Age != 41 {
			t.Errorf("Update Returning = %+v, want one Bo aged 41", got)
		}
	})

	t.Run("correlated UPDATE … FROM (set many rows from a source)", func(t *testing.T) {
		// age += adjustments.delta, joined per person — different value per row.
		n, err := query.Update[Person](db).
			SetExpr("age", "age + adj.delta").
			From("adjustments AS adj").
			Where("people.id = adj.person_id").
			Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("UPDATE…FROM affected %d rows, want 2", n)
		}
		// Bo was 41 (from the prior subtest) + 10 = 51; Di 55 - 5 = 50.
		bo, _ := query.Select[Person](db).Where("name = ?", "Bo").First(ctx)
		di, _ := query.Select[Person](db).Where("name = ?", "Di").First(ctx)
		if bo.Age != 51 || di.Age != 50 {
			t.Errorf("after UPDATE…FROM: Bo=%d Di=%d, want 51/50", bo.Age, di.Age)
		}
	})

	t.Run("Delete Returning yields the deleted rows", func(t *testing.T) {
		got, err := query.Delete[Person](db).
			Filter(query.Col[int64]("age").Ge(50)).
			Returning(ctx) // Bo(51), Cy(70), Di(50)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Errorf("Delete Returning removed %d rows, want 3", len(got))
		}
		left, _ := query.Select[Person](db).All(ctx)
		if len(left) != 1 || left[0].Name != "Ada" {
			t.Errorf("after delete, remaining = %+v, want just Ada", left)
		}
	})

	t.Run("WHERE-less write is refused", func(t *testing.T) {
		if _, err := query.Update[Person](db).Set("age", 1).Exec(ctx); err == nil {
			t.Error("WHERE-less Update should error")
		}
		if _, err := query.Delete[Person](db).Exec(ctx); err == nil {
			t.Error("WHERE-less Delete should error")
		}
	})
}
