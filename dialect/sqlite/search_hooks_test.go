package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	_ "gosqlite.org/vec"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// hookDoc keeps the embedding sidecar-only (orm:"-"), so the vector index is
// hook-synced from the ORM write path rather than by a trigger.
type hookDoc struct {
	ID        int64
	Title     string
	Embedding []float32 `orm:"-"`
}

func (hookDoc) TableName() string { return "hook_docs" }

func (hookDoc) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.Vector("Embedding", 3).WithMetric(orm.L2)}
}

func TestSearchHooks_VectorSyncsThroughRepo(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := orm.AutoMigrate[hookDoc](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	repo := orm.NewRepo[hookDoc](db)

	vecCount := func() int {
		rows, err := db.QueryContext(ctx, `SELECT rowid FROM hook_docs_vec`)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
		}
		return n
	}
	nearest := func(v ...float32) int64 {
		rows, err := db.QueryContext(ctx,
			`SELECT rowid FROM hook_docs_vec WHERE embedding MATCH ? ORDER BY distance LIMIT 1`, vblob(v...))
		if err != nil {
			t.Fatalf("knn: %v", err)
		}
		defer rows.Close()
		var id int64
		if rows.Next() {
			_ = rows.Scan(&id)
		}
		return id
	}

	// Create through the Repo: the AfterCreate choke point must upsert the sidecar.
	a := &hookDoc{Title: "fox", Embedding: []float32{1, 0, 0}}
	b := &hookDoc{Title: "dog", Embedding: []float32{0, 1, 0}}
	for _, d := range []*hookDoc{a, b} {
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if n := vecCount(); n != 2 {
		t.Fatalf("after Create, vec rows = %d, want 2", n)
	}
	if got := nearest(1, 0, 0); got != a.ID {
		t.Errorf("KNN nearest (1,0,0) = %d, want %d", got, a.ID)
	}

	// Update through the Repo: AfterUpdate re-syncs the new embedding.
	a.Embedding = []float32{0, 0, 1}
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := nearest(0, 0, 1); got != a.ID {
		t.Errorf("after Update, KNN nearest (0,0,1) = %d, want %d", got, a.ID)
	}

	// A partial update without the embedding must NOT clobber the sidecar.
	a.Title, a.Embedding = "renamed", nil
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if got := nearest(0, 0, 1); got != a.ID {
		t.Errorf("after empty-embedding update, KNN nearest (0,0,1) = %d, want %d (sidecar should be untouched)", got, a.ID)
	}

	// Hard delete through the Repo removes the row from the sidecar.
	if err := repo.Delete(ctx, b); err != nil { // hookDoc has no soft-delete column -> hard delete
		t.Fatalf("delete: %v", err)
	}
	if n := vecCount(); n != 1 {
		t.Errorf("after Delete, vec rows = %d, want 1", n)
	}
}
