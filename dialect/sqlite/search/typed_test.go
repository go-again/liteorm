package search_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	_ "gosqlite.org/vec"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/search"
	"liteorm.org/orm"
)

type article struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32    `orm:"-"` // sidecar-only -> hook-synced vector
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
}

func (article) TableName() string { return "articles" }

func (article) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body"), // text columns -> external-content + triggers
		orm.Vector("Embedding", 3).WithMetric(orm.L2),
	}
}

func ids(hits []search.Hit[article]) []int64 {
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.Model.ID
	}
	return out
}

func TestTypedSearch_KNN_Match_Fuse(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "typed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := orm.AutoMigrate[article](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	repo := orm.NewRepo[article](db)
	for _, a := range []*article{
		{Title: "quick brown fox", Body: "the fox jumps", Embedding: []float32{1, 0, 0}},
		{Title: "lazy dog", Body: "the dog sleeps", Embedding: []float32{0, 1, 0}},
		{Title: "fox meets dog", Body: "a fox and a dog", Embedding: []float32{0.7, 0.7, 0}},
	} {
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// KNN returns models nearest-first, with the loaded model populated.
	near, err := search.For[article](db).Vector(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	if len(near) != 2 || near[0].Model.ID != 1 {
		t.Fatalf("KNN nearest = %v, want first id 1", ids(near))
	}
	if near[0].Model.Title != "quick brown fox" {
		t.Errorf("KNN top model not loaded: title = %q", near[0].Model.Title)
	}

	// Match returns the full-text hit's model.
	hits, err := search.For[article](db).FullText(ctx, search.Term("lazy"), 5)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(hits) != 1 || hits[0].Model.ID != 2 {
		t.Fatalf("Match 'lazy' = %v, want [2]", ids(hits))
	}

	// Fuse blends both rankings; a doc strong in either surfaces.
	fused, err := search.For[article](db).Hybrid(ctx, []float32{0, 1, 0}, search.Term("dog"), 5)
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if len(fused) == 0 || !slices.Contains(ids(fused), 2) {
		t.Errorf("Fuse results = %v, want to include id 2", ids(fused))
	}

	// Soft delete excludes the row from typed results (loaded through the orm Repo,
	// which honors the soft-delete scope) even though it lingers in the sidecars.
	two, _ := repo.Get(ctx, 2)
	if err := repo.Delete(ctx, &two); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	hits, err = search.For[article](db).FullText(ctx, search.Term("lazy"), 5)
	if err != nil {
		t.Fatalf("Match after delete: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("Match 'lazy' after soft delete = %v, want none", ids(hits))
	}
	near, err = search.For[article](db).Vector(ctx, []float32{0, 1, 0}, 3)
	if err != nil {
		t.Fatalf("KNN after delete: %v", err)
	}
	if slices.Contains(ids(near), 2) {
		t.Errorf("KNN after soft delete returned the deleted id 2: %v", ids(near))
	}
}

// kbDoc has a STRING primary key; its vector sidecar is keyed by that string.
type kbDoc struct {
	Slug      string `orm:"slug,pk"`
	Title     string
	Embedding []float32 `orm:"-"`
}

func (kbDoc) TableName() string { return "kb_docs" }

func (kbDoc) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.Vector("Embedding", 3).WithMetric(orm.L2)}
}

func TestTypedSearch_StringKeyVector(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.AutoMigrate[kbDoc](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	repo := orm.NewRepo[kbDoc](db)
	for _, d := range []*kbDoc{
		{Slug: "fox", Title: "Fox", Embedding: []float32{1, 0, 0}},
		{Slug: "dog", Title: "Dog", Embedding: []float32{0, 1, 0}},
	} {
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %s: %v", d.Slug, err)
		}
	}
	near, err := search.For[kbDoc](db).Vector(ctx, []float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	if len(near) != 1 || near[0].Model.Slug != "fox" {
		t.Fatalf("string-key KNN nearest = %+v, want slug 'fox'", near)
	}
	if near[0].Model.Title != "Fox" {
		t.Errorf("loaded model not populated: %+v", near[0].Model)
	}
}
