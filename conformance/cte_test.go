package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

type Cat struct {
	ID       int64
	ParentID int64
	Name     string
}

func (Cat) TableName() string { return "cats" }

// TestCTEAndSubqueryFromLive exercises the Phase-3 structural additions —
// recursive CTEs, derived-table FROM, and join-on-subquery — against a live
// database, proving they execute (not just render).
func TestCTEAndSubqueryFromLive(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "cte.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE cats (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL, name TEXT NOT NULL)`,
		// tree: root(1) → a(2) → a1(4); root(1) → b(3)
		`INSERT INTO cats (id,parent_id,name) VALUES (1,0,'root'),(2,1,'a'),(3,1,'b'),(4,2,'a1')`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(cs []Cat) []int64 {
		out := make([]int64, len(cs))
		for i, c := range cs {
			out[i] = c.ID
		}
		return out
	}

	t.Run("recursive CTE walks the subtree", func(t *testing.T) {
		anchor := query.Select[Cat](db).Where("id = ?", 1)
		recur := query.Select[Cat](db).Join("JOIN subtree ON cats.parent_id = subtree.id")
		got, err := query.Select[Cat](db).
			WithRecursive("subtree", anchor.UnionAll(recur)).
			From("subtree").
			OrderBy("id").
			All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 4 || g[0] != 1 || g[3] != 4 {
			t.Errorf("recursive descendants of root = %v, want [1 2 3 4]", g)
		}
	})

	t.Run("derived-table FROM (subquery)", func(t *testing.T) {
		sub := query.Select[Cat](db).Where("id > ?", 1)
		got, err := query.FromSubquery[Cat](db, "d", sub).
			Filter(query.Col[string]("name").Eq("a")).
			All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 1 || g[0] != 2 {
			t.Errorf("derived-table filter = %v, want [2]", g)
		}
	})

	t.Run("join on a subquery (cats that are a parent)", func(t *testing.T) {
		kids := query.Select[Cat](db).Project("DISTINCT parent_id").Where("parent_id > ?", 0)
		got, err := query.Select[Cat](db).
			JoinSub("INNER JOIN", "k", kids, "k.parent_id = cats.id").
			OrderBy("cats.id").
			All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 2 || g[0] != 1 || g[1] != 2 {
			t.Errorf("parents via join-subquery = %v, want [1 2]", g)
		}
	})
}
