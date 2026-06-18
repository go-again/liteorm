package search_test

import (
	"context"
	"path/filepath"
	"testing"

	_ "gosqlite.org/vec"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/search"
	"liteorm.org/orm"
)

// intDoc has a plain `int` primary key (a valid SQLite rowid alias), not int64 —
// the case the earlier PKType=="int64" check misclassified as a string key.
type intDoc struct {
	ID        int
	Title     string
	Embedding []float32 `orm:"-"`
}

func (intDoc) TableName() string { return "int_docs" }

func (intDoc) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.Vector("Embedding", 3).WithMetric(orm.L2)}
}

// TestSearch_IntPKVector locks the fix for the misclassification of non-int64
// integer PKs: an `int` PK must use the rowid-keyed vec0 path end to end.
func TestSearch_IntPKVector(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "intpk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.AutoMigrate[intDoc](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	repo := orm.NewRepo[intDoc](db)
	for _, d := range []*intDoc{
		{Title: "fox", Embedding: []float32{1, 0, 0}},
		{Title: "dog", Embedding: []float32{0, 1, 0}},
	} {
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %s: %v", d.Title, err)
		}
	}
	near, err := search.For[intDoc](db).Vector(ctx, []float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Vector: %v", err)
	}
	if len(near) != 1 || near[0].Model.Title != "fox" {
		t.Fatalf("int-PK vector nearest = %+v, want title 'fox'", near)
	}
}
