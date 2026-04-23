package search_test

import (
	"context"
	"path/filepath"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/search"
)

// Doc is the model that owns the rows; the vec/fts indexes are sidecars keyed by
// Doc.ID.
type Doc struct {
	ID    int64
	Title string
}

func (Doc) TableName() string { return "docs" }

// corpus: a tiny 3-dimensional embedding space plus titles, chosen so the
// nearest-neighbour and full-text results are unambiguous.
var corpus = []struct {
	id    int64
	title string
	emb   []float32
}{
	{1, "the quick brown fox", []float32{1, 0, 0}},
	{2, "lazy dog sleeps all day", []float32{0, 1, 0}},
	{3, "a fox and a dog", []float32{0.7, 0.7, 0}},
	{4, "moon and distant stars", []float32{0, 0, 1}},
}

func setup(t *testing.T) (context.Context, *liteorm.DB, *search.Vector, *search.FullText) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(ctx, `CREATE TABLE docs (id INTEGER PRIMARY KEY, title TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	vidx, err := search.NewVector(ctx, db, "doc_vecs", 3, search.Cosine)
	if err != nil {
		t.Fatal(err)
	}
	fidx, err := search.NewFullText(ctx, db, "doc_fts")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range corpus {
		if _, err := db.ExecContext(ctx, `INSERT INTO docs (id, title) VALUES (?, ?)`, d.id, d.title); err != nil {
			t.Fatal(err)
		}
		if err := vidx.Add(ctx, d.id, d.emb); err != nil {
			t.Fatal(err)
		}
		if err := fidx.Add(ctx, d.id, d.title); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db, vidx, fidx
}

func TestVectorSearch(t *testing.T) {
	ctx, _, vidx, _ := setup(t)
	got, err := vidx.Search(ctx, []float32{1, 0.1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	// nearest to (1,0.1,0): doc 1 (1,0,0), then doc 3 (0.7,0.7,0).
	if len(got) != 2 || got[0] != 1 {
		t.Fatalf("vector search = %v, want first=1 of 2", got)
	}
}

func TestFullTextSearch(t *testing.T) {
	ctx, _, _, fidx := setup(t)
	got, err := fidx.Search(ctx, search.Term("moon"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("fts 'moon' = %v, want [4]", got)
	}
	got, err = fidx.Search(ctx, search.Term("dog"), 5)
	if err != nil {
		t.Fatal(err)
	}
	// "dog" appears in docs 2 and 3.
	if !containsAll(got, 2, 3) {
		t.Fatalf("fts 'dog' = %v, want to contain 2 and 3", got)
	}
}

func TestHybridFusion(t *testing.T) {
	ctx, db, vidx, fidx := setup(t)
	// Vector query points at doc 1's region; full-text query matches "dog"
	// (docs 2,3). Fusion should surface doc 3 highly (it ranks in BOTH: near
	// doc 1 in vector space AND contains "dog"), demonstrating the value of
	// hybrid over either modality alone.
	fused, err := search.Hybrid(ctx, vidx, fidx, []float32{1, 0.1, 0}, search.Term("dog"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(fused) == 0 {
		t.Fatal("hybrid returned no results")
	}
	rank := map[int64]int{}
	for i, s := range fused {
		rank[s.Key] = i
	}
	r3, ok := rank[3]
	if !ok {
		t.Fatalf("hybrid %v should include doc 3 (ranks in both modalities)", fused)
	}
	// doc 3 matches "dog" AND sits near the vector query, so it must outrank the
	// vector-only docs (1 and 4). That lift is exactly what fusion buys: neither
	// modality alone ranks doc 3 first, but combined it rises to the top tier.
	if r1, ok := rank[1]; ok && r3 > r1 {
		t.Fatalf("hybrid %v: doc 3 (both modalities) should outrank doc 1 (vector only)", fused)
	}
	if r4, ok := rank[4]; ok && r3 > r4 {
		t.Fatalf("hybrid %v: doc 3 (both modalities) should outrank doc 4 (vector only)", fused)
	}
	if r3 > 1 {
		t.Fatalf("hybrid %v: doc 3 should land in the top tier (got rank %d)", fused, r3)
	}

	// Load fetches the model rows in fused order.
	rows, err := search.LoadScored[Doc](ctx, db, fused)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(fused) {
		t.Fatalf("loaded %d rows, want %d", len(rows), len(fused))
	}
	for i, r := range rows {
		if r.ID != fused[i].Key {
			t.Fatalf("row %d id=%d, want %d (order not preserved)", i, r.ID, fused[i].Key)
		}
		if r.Title == "" {
			t.Fatalf("row %d has empty title (not scanned)", i)
		}
	}
}

func containsAll(s []int64, want ...int64) bool {
	set := map[int64]bool{}
	for _, x := range s {
		set[x] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
