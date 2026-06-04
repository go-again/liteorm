// Command search is a tour of liteorm's SQLite-only advanced search: vector
// (sqlite-vec) nearest-neighbour, full-text (FTS5) keyword/phrase queries, and
// the hybrid reciprocal-rank-fusion that combines them — plus the at-rest
// encryption passthrough. The vec/fts indexes are sidecars keyed by the Doc
// model's primary key; a search returns ranked keys and Load fetches the rows.
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
)

type Doc struct {
	ID    int64
	Title string
	Body  string
}

func (Doc) TableName() string { return "docs" }

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
	docs, err := search.LoadScored[Doc](ctx, db, fused)
	if err != nil {
		return err
	}
	for i, d := range docs {
		fmt.Printf("  %.4f  %s\n", fused[i].Score, d.Title)
	}
	fmt.Println("  → 'The Software Behind Spaceflight' tops the list: it ranks in BOTH")
	fmt.Println("    the vector neighbourhood and the text query, which neither alone ranks first.")

	// ---- At-rest encryption ----
	section("Encryption: write encrypted, reopen with the key")
	encPath := filepath.Join(dir, "secret.db")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	enc, err := sqlite.OpenEncrypted(encPath, key)
	if err != nil {
		return err
	}
	if _, err := enc.ExecContext(ctx, `CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT)`); err != nil {
		return err
	}
	if _, err := enc.ExecContext(ctx, `INSERT INTO notes (text) VALUES (?)`, "encrypted at rest"); err != nil {
		return err
	}
	_ = enc.Close()
	reopened, err := sqlite.OpenEncrypted(encPath, key)
	if err != nil {
		return err
	}
	defer reopened.Close()
	var text string
	if err := scanOne(ctx, reopened, &text, `SELECT text FROM notes WHERE id = 1`); err != nil {
		return err
	}
	fmt.Printf("  reopened with key → %q (the on-disk file is ciphertext)\n", text)

	fmt.Println()
	return nil
}

func printDocs(ctx context.Context, db *liteorm.DB, keys []int64) error {
	docs, err := search.Load[Doc](ctx, db, keys)
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

func scanOne(ctx context.Context, db *liteorm.DB, dst *string, q string) error {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return rows.Err()
	}
	return rows.Scan(dst)
}
