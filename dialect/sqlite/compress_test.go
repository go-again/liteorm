package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gosqlite "gosqlite.org"
	"gosqlite.org/vfs/compress"
	"liteorm.org/dialect/sqlite"
)

// TestOpenCompressed_RoundTrip writes compressible rows through a compressed
// database, closes it (which recompresses the working copy over the path), checks
// the on-disk file is much smaller than the logical content, then reopens it and
// reads the rows back — proving the snapshot round-trip works under the ORM.
func TestOpenCompressed_RoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db.az")

	db, err := sqlite.OpenCompressedConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		compress.Options{Level: compress.CompressionBest},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	row := strings.Repeat("the quick brown fox jumps over the lazy dog ", 64) // ~2.8 KB, very compressible
	const n = 500
	for range n {
		if _, err := db.ExecContext(ctx, `INSERT INTO notes (body) VALUES (?)`, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil { // recompresses the working copy over path
		t.Fatal(err)
	}

	// The on-disk file is a compressed frame: far smaller than the logical content
	// and not a raw SQLite database (no "SQLite format 3" header).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	logical := int64(len(row)) * n
	if int64(len(raw)) >= logical/4 {
		t.Fatalf("on-disk file = %d bytes, want << logical %d (not compressed)", len(raw), logical)
	}
	if strings.HasPrefix(string(raw), "SQLite format 3") {
		t.Fatal("on-disk file is a raw SQLite database, not a compressed frame")
	}

	// Reopen with the default options (level auto-detected) and read it back.
	db2, err := sqlite.OpenCompressed(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	rows, err := db2.QueryContext(ctx, `SELECT count(*) FROM notes`)
	if err != nil {
		t.Fatalf("reopen + count: %v", err)
	}
	defer rows.Close()
	var got int
	if rows.Next() {
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
	}
	if got != n {
		t.Fatalf("row count = %d, want %d", got, n)
	}
}

// TestOpenCompressed_RejectsMemory confirms the on-disk requirement surfaces as
// an error rather than silently doing nothing.
func TestOpenCompressed_RejectsMemory(t *testing.T) {
	if _, err := sqlite.OpenCompressedConfig(gosqlite.Config{Path: gosqlite.InMemory}, compress.Options{}); err == nil {
		t.Fatal("an in-memory compressed database should be rejected")
	}
}
