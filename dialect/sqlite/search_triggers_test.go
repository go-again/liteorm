package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	_ "gosqlite.org/vec"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// trigDoc stores the embedding as a real column, so the vector index is
// trigger-synced (along with the full-text index over Title).
type trigDoc struct {
	ID        int64
	Title     string
	Embedding []byte // stored blob -> vector trigger mode
}

func (trigDoc) TableName() string { return "trig_docs" }

func (trigDoc) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title"),
		orm.Vector("Embedding", 3).WithMetric(orm.L2),
	}
}

func TestSearchTriggers_AutoSyncOnRawWrites(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "trig.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := orm.AutoMigrate[trigDoc](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	scalarIDs := func(q string, args ...any) []int64 {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return ids
	}
	count := func(table string) int {
		ids := scalarIDs(`SELECT rowid FROM ` + table)
		return len(ids)
	}
	ftsMatch := func(q string) []int64 {
		return scalarIDs(`SELECT rowid FROM trig_docs_fts WHERE trig_docs_fts MATCH ? ORDER BY rowid`, q)
	}
	knn := func(v ...float32) int64 {
		ids := scalarIDs(`SELECT rowid FROM trig_docs_vec WHERE embedding MATCH ? ORDER BY distance LIMIT 1`, vblob(v...))
		if len(ids) == 0 {
			return 0
		}
		return ids[0]
	}
	first := func(ids []int64) int64 {
		if len(ids) == 0 {
			return 0
		}
		return ids[0]
	}

	// Plain raw INSERTs — no manual sidecar maintenance. Triggers must populate
	// BOTH sidecars.
	exec(`INSERT INTO trig_docs(id, title, embedding) VALUES (1, 'quick brown fox', ?)`, vblob(1, 0, 0))
	exec(`INSERT INTO trig_docs(id, title, embedding) VALUES (2, 'lazy dog sleeps', ?)`, vblob(0, 1, 0))
	exec(`INSERT INTO trig_docs(id, title, embedding) VALUES (3, 'fox and dog', ?)`, vblob(0.7, 0.7, 0))

	if got := first(ftsMatch("quick")); got != 1 {
		t.Errorf("after insert, FTS MATCH 'quick' = %d, want 1", got)
	}
	if got := knn(1, 0, 0); got != 1 {
		t.Errorf("after insert, vec KNN nearest (1,0,0) = %d, want 1", got)
	}
	if n := count("trig_docs_vec"); n != 3 {
		t.Errorf("after insert, vec rows = %d, want 3", n)
	}

	// UPDATE re-syncs both indexes.
	exec(`UPDATE trig_docs SET title = 'zebra stripes', embedding = ? WHERE id = 1`, vblob(0, 0, 1))
	if got := ftsMatch("quick"); len(got) != 0 {
		t.Errorf("after update, FTS MATCH 'quick' = %v, want none", got)
	}
	if got := first(ftsMatch("zebra")); got != 1 {
		t.Errorf("after update, FTS MATCH 'zebra' = %d, want 1", got)
	}
	if got := knn(0, 0, 1); got != 1 {
		t.Errorf("after update, vec KNN nearest (0,0,1) = %d, want 1", got)
	}

	// DELETE removes from both.
	exec(`DELETE FROM trig_docs WHERE id = 2`)
	if got := ftsMatch("lazy"); len(got) != 0 {
		t.Errorf("after delete, FTS MATCH 'lazy' = %v, want none", got)
	}
	if n := count("trig_docs_vec"); n != 2 {
		t.Errorf("after delete, vec rows = %d, want 2", n)
	}

	// Multi-row INSERT fires the row triggers per row (the bulk/raw case gorm
	// callbacks can't cover).
	exec(`INSERT INTO trig_docs(id, title, embedding) VALUES (10, 'alpha', ?), (11, 'beta', ?)`,
		vblob(1, 1, 1), vblob(0.2, 0.2, 0.2))
	if n := count("trig_docs_vec"); n != 4 {
		t.Errorf("after bulk insert, vec rows = %d, want 4", n)
	}
	if got := first(ftsMatch("alpha")); got != 10 {
		t.Errorf("after bulk insert, FTS MATCH 'alpha' = %d, want 10", got)
	}
}
