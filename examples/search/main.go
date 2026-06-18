// Command search is a tour of liteorm's SQLite-only advanced search: vector
// (sqlite-vec) nearest-neighbour, full-text (FTS5) keyword/phrase queries, and
// the hybrid reciprocal-rank-fusion that combines them.
//
// It shows two layers. The DECLARATIVE layer is the recommended path: a model
// declares its indexes (a SearchIndexes method, or `vec:`/`fts:` struct tags),
// AutoMigrate provisions the sidecars and keeps them in sync on every write, and
// the typed searcher — search.For[T](db).Vector / .FullText / .Hybrid — returns
// ranked models. The LOW-LEVEL layer drives the sidecars by hand
// (NewVector/NewFullText + Add) for callers that manage the index lifecycle.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/search"
	"liteorm.org/orm"
)

type Doc struct {
	ID    int64
	Title string
	Body  string
}

func (Doc) TableName() string { return "docs" }

// Article is the DECLARATIVE model: it declares a full-text index over its text
// columns and a vector index over the embedding. AutoMigrate creates the base
// table, the FTS5 and vec0 sidecars, and the triggers/hooks that keep them
// current — so plain Repo.Create/Update/Delete need no manual index calls.
type Article struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32 `orm:"-"` // sidecar-only; synced from the ORM write path
}

func (Article) TableName() string { return "articles" }

func (Article) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body"),
		orm.Vector("Embedding", 5).WithMetric(orm.Cosine),
	}
	// Equivalently, declare per-field tags: Title/Body with `fts:"..."` and the
	// embedding with `vec:"dim=5;metric=cosine"`.
}

// Each doc carries a toy 5-dimensional "topic" embedding over
// [animals, space, cooking, tech, music]. Real systems use a model; the shape
// is the same: an int64 key and a []float32.
var corpus = []struct {
	doc Doc
	emb []float32
}{
	{Doc{1, "Foxes of the Northern Woods", "Tracking red foxes and their dens across the boreal forest."}, []float32{1, 0, 0, 0, 0}},
	{Doc{2, "Apollo and the Race to the Moon", "The Apollo program landed astronauts on the moon in 1969."}, []float32{0, 1, 0, 0, 0}},
	{Doc{3, "Sourdough: A Baker's Guide", "A practical guide to wild yeast, hydration, and a crackling crust."}, []float32{0, 0, 1, 0, 0}},
	{Doc{4, "Rockets, Engines, and Orbital Mechanics", "How rocket nozzles, combustion, and orbital insertion work."}, []float32{0, 0.7, 0, 0.7, 0}},
	{Doc{5, "Jazz Improvisation Basics", "Comping, scales, and trading fours in a small jazz combo."}, []float32{0, 0, 0, 0, 1}},
	{Doc{6, "The Software Behind Spaceflight", "The flight software and avionics that keep a spacecraft on course."}, []float32{0, 0.6, 0, 0.8, 0}},
}

// spaceQuery is an embedding pointed at the "space" topic.
var spaceQuery = []float32{0, 1, 0, 0, 0}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-search-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "library.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	// ===== Declarative layer (recommended): declare → migrate → write → search.
	if err := declarative(ctx, db); err != nil {
		return err
	}

	// ===== Low-level building blocks: drive the vec/fts sidecars by hand, for
	// callers that own the index lifecycle.
	section("Low-level: manage the sidecars yourself")
	if _, err := db.ExecContext(ctx, `CREATE TABLE docs (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		body  TEXT NOT NULL
	)`); err != nil {
		return err
	}

	// Two sidecar indexes, both keyed by docs.id.
	vidx, err := search.NewVector(ctx, db, "doc_vecs", 5, search.Cosine)
	if err != nil {
		return err
	}
	fidx, err := search.NewFullText(ctx, db, "doc_fts")
	if err != nil {
		return err
	}
	for _, c := range corpus {
		if _, err := db.ExecContext(ctx, `INSERT INTO docs (id, title, body) VALUES (?, ?, ?)`, c.doc.ID, c.doc.Title, c.doc.Body); err != nil {
			return err
		}
		if err := vidx.Add(ctx, c.doc.ID, c.emb); err != nil {
			return err
		}
		if err := fidx.Add(ctx, c.doc.ID, c.doc.Title+" "+c.doc.Body); err != nil {
			return err
		}
	}

	// ---- Vector nearest-neighbour ----
	section("Vector search: nearest to the 'space' topic")
	vkeys, err := vidx.Search(ctx, spaceQuery, 3)
	if err != nil {
		return err
	}
	if err := printDocs(ctx, db, vkeys); err != nil {
		return err
	}

	// ---- Full-text: term, phrase, boolean ----
	section("Full-text: term 'rocket'")
	fk, err := fidx.Search(ctx, search.Term("rocket"), 5)
	if err != nil {
		return err
	}
	if err := printDocs(ctx, db, fk); err != nil {
		return err
	}

	section("Full-text: 'software' AND 'flight'")
	fk, err = fidx.Search(ctx, search.And(search.Term("software"), search.Term("flight")), 5)
	if err != nil {
		return err
	}
	if err := printDocs(ctx, db, fk); err != nil {
		return err
	}

	// ---- Hybrid fusion: vector 'space' + text 'software' ----
	section("Hybrid (RRF): vector 'space' ⊕ text 'software'")
	fused, err := search.Hybrid(ctx, vidx, fidx, spaceQuery, search.Term("software"), 4)
	if err != nil {
		return err
	}
	docs, err := search.FetchScored[Doc](ctx, db, fused)
	if err != nil {
		return err
	}
	for i, d := range docs {
		fmt.Printf("  %.4f  %s\n", fused[i].Score, d.Title)
	}
	fmt.Println("  → 'The Software Behind Spaceflight' tops the list: it ranks in BOTH")
	fmt.Println("    the vector neighbourhood and the text query, which neither alone ranks first.")

	fmt.Println()
	return nil
}

// declarative is the recommended path: declare the indexes on the model,
// AutoMigrate, write through the Repo (the sidecars stay in sync automatically),
// and search with the typed helpers that return models in ranked order.
func declarative(ctx context.Context, db *liteorm.DB) error {
	if err := orm.AutoMigrate[Article](ctx, db); err != nil {
		return err
	}
	repo := orm.NewRepo[Article](db)
	for _, c := range corpus {
		a := &Article{Title: c.doc.Title, Body: c.doc.Body, Embedding: c.emb}
		if err := repo.Create(ctx, a); err != nil { // inserts the row AND syncs both sidecars
			return err
		}
	}

	section("Declarative: nearest to the 'space' topic (search.For[T].Vector returns models)")
	near, err := search.For[Article](db).Vector(ctx, spaceQuery, 3)
	if err != nil {
		return err
	}
	for _, h := range near {
		fmt.Printf("  %.4f  %s\n", h.Score, h.Model.Title)
	}

	section("Declarative: full-text 'software' AND 'flight'")
	hits, err := search.For[Article](db).FullText(ctx, search.And(search.Term("software"), search.Term("flight")), 5)
	if err != nil {
		return err
	}
	printHits(hits)

	section("Declarative: hybrid (RRF) vector 'space' ⊕ text 'software'")
	fused, err := search.For[Article](db).Hybrid(ctx, spaceQuery, search.Term("software"), 4)
	if err != nil {
		return err
	}
	for _, h := range fused {
		fmt.Printf("  %.4f  %s\n", h.Score, h.Model.Title)
	}
	fmt.Println("  → 'The Software Behind Spaceflight' tops the hybrid: strong in BOTH modalities.")
	return nil
}

func printHits(hits []search.Hit[Article]) {
	if len(hits) == 0 {
		fmt.Println("  (no matches)")
	}
	for _, h := range hits {
		fmt.Printf("  #%d  %s\n", h.Model.ID, h.Model.Title)
	}
}

func printDocs(ctx context.Context, db *liteorm.DB, keys []int64) error {
	docs, err := search.Fetch[Doc](ctx, db, keys)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		fmt.Println("  (no matches)")
	}
	for _, d := range docs {
		fmt.Printf("  #%d  %s\n", d.ID, d.Title)
	}
	return nil
}
