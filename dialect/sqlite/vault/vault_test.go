package vault_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gosqlite "gosqlite.org"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite/vault"
)

func write(t *testing.T, db *liteorm.DB, body string, n int) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	for range n {
		if _, err := db.ExecContext(ctx, `INSERT INTO t (body) VALUES (?)`, body); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, db *liteorm.DB) int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT count(*) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var n int
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	return n
}

// TestVault_LiveRoundTrip proves the live container round-trips and is compressed
// on disk (far smaller than logical, and not a raw SQLite file).
func TestVault_LiveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db.vault")
	body := strings.Repeat("the quick brown fox jumps over the lazy dog ", 64)
	const n = 1000

	db, err := vault.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, db, body, n)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(raw), "SQLite format 3") {
		t.Fatal("on-disk file is a raw SQLite database, not a vault container")
	}
	logical := int64(len(body)) * n
	if int64(len(raw)) >= logical/2 {
		t.Fatalf("on-disk %d bytes is not << logical %d (not compressed)", len(raw), logical)
	}

	db2, err := vault.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := count(t, db2); got != n {
		t.Fatalf("count = %d, want %d", got, n)
	}
}

// TestVault_Encrypted proves a compressed+encrypted live container leaves no
// plaintext on disk, reopens with the key, and rejects a wrong key.
func TestVault_Encrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enc.db.vault")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	const secret = "classified-payload-xyz"

	db, err := vault.OpenConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		vault.Options{Level: vault.CompressionBest, Key: key},
	)
	if err != nil {
		t.Fatal(err)
	}
	write(t, db, strings.Repeat(secret, 32), 200)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("plaintext leaked into the encrypted vault on disk")
	}

	db2, err := vault.OpenConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		vault.Options{Key: key},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := count(t, db2); got != 200 {
		t.Fatalf("count = %d, want 200", got)
	}

	wrong := make([]byte, 32)
	for i := range wrong {
		wrong[i] = 0xAA
	}
	if bad, err := vault.OpenConfig(gosqlite.Config{Path: path}, vault.Options{Key: wrong}); err == nil {
		if _, err := bad.QueryContext(context.Background(), `SELECT count(*) FROM t`); err == nil {
			t.Fatal("wrong key read the encrypted vault")
		}
		_ = bad.Close()
	}
}

// TestVault_Snapshot exercises the archival snapshot model round-trip.
func TestVault_Snapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.db.vault")
	db, err := vault.OpenSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, db, strings.Repeat("compress me ", 100), 500)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := vault.OpenSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := count(t, db2); got != 500 {
		t.Fatalf("snapshot count = %d, want 500", got)
	}
}
