package sqlite_test

import (
	"context"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"

	_ "gosqlite.org/vec" // side-effect: registers the vec0 extension
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// provDoc declares both a full-text and a vector sidecar via the SearchIndexes
// method; the embedding is sidecar-only (orm:"-"), the text columns are real.
type provDoc struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32 `orm:"-"`
}

func (provDoc) TableName() string { return "prov_docs" }

func (provDoc) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body").WithTokenizer("unicode61"),
		orm.Vector("Embedding", 3).WithMetric(orm.Cosine),
	}
}

func vblob(v ...float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

func TestAutoMigrate_ProvisionsSearchSidecars(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "prov.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	exists := func(name string) bool {
		rows, err := db.QueryContext(ctx, `SELECT 1 FROM sqlite_master WHERE name = ?`, name)
		if err != nil {
			t.Fatalf("sqlite_master: %v", err)
		}
		defer rows.Close()
		return rows.Next()
	}

	// AutoMigrate should create the base table AND both sidecars.
	if err := orm.AutoMigrate[provDoc](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	for _, name := range []string{"prov_docs", "prov_docs_fts", "prov_docs_vec"} {
		if !exists(name) {
			t.Errorf("expected table %q to exist after AutoMigrate", name)
		}
	}

	// Idempotent: a second run must not error (CREATE ... IF NOT EXISTS).
	if err := orm.AutoMigrate[provDoc](ctx, db); err != nil {
		t.Fatalf("second AutoMigrate (should be a no-op): %v", err)
	}

	// Seed the base table.
	corpus := []struct {
		id    int64
		title string
		emb   []byte
	}{
		{1, "quick brown fox", vblob(1, 0, 0)},
		{2, "lazy dog sleeps", vblob(0, 1, 0)},
		{3, "a fox and a dog", vblob(0.7, 0.7, 0)},
	}
	for _, c := range corpus {
		if _, err := db.ExecContext(ctx, `INSERT INTO prov_docs(id, title, body) VALUES (?, ?, '')`, c.id, c.title); err != nil {
			t.Fatalf("seed base row: %v", err)
		}
	}

	// The vec0 sidecar is a usable vec0 table: insert embeddings, KNN-search them.
	for _, c := range corpus {
		if _, err := db.ExecContext(ctx, `INSERT INTO prov_docs_vec(rowid, embedding) VALUES (?, ?)`, c.id, c.emb); err != nil {
			t.Fatalf("insert into vec sidecar: %v", err)
		}
	}
	var nearest int64
	rows, err := db.QueryContext(ctx,
		`SELECT rowid FROM prov_docs_vec WHERE embedding MATCH ? ORDER BY distance LIMIT 1`, vblob(1, 0, 0))
	if err != nil {
		t.Fatalf("vec KNN: %v", err)
	}
	if rows.Next() {
		_ = rows.Scan(&nearest)
	}
	rows.Close()
	if nearest != 1 {
		t.Errorf("vec KNN nearest to (1,0,0) = %d, want 1", nearest)
	}

	// The FTS5 sidecar is external-content (content='prov_docs'): backfill from the
	// base table via 'rebuild', then MATCH resolves base rowids.
	if _, err := db.ExecContext(ctx, `INSERT INTO prov_docs_fts(prov_docs_fts) VALUES('rebuild')`); err != nil {
		t.Fatalf("fts rebuild: %v", err)
	}
	var hit int64
	frows, err := db.QueryContext(ctx, `SELECT rowid FROM prov_docs_fts WHERE prov_docs_fts MATCH ? LIMIT 1`, "quick")
	if err != nil {
		t.Fatalf("fts MATCH: %v", err)
	}
	if frows.Next() {
		_ = frows.Scan(&hit)
	}
	frows.Close()
	if hit != 1 {
		t.Errorf("fts MATCH 'quick' = %d, want 1", hit)
	}

	// DropSearchIndexes removes both sidecars, leaving the base table.
	if err := orm.DropSearchIndexes[provDoc](ctx, db); err != nil {
		t.Fatalf("DropSearchIndexes: %v", err)
	}
	if !exists("prov_docs") {
		t.Error("base table should survive DropSearchIndexes")
	}
	for _, name := range []string{"prov_docs_fts", "prov_docs_vec"} {
		if exists(name) {
			t.Errorf("sidecar %q should be gone after DropSearchIndexes", name)
		}
	}
}
