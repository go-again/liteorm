package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	gosqlite "gosqlite.org"
	"gosqlite.org/vfs/crypto"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// TestOpenEncrypted_RoundTrip writes through an encrypted database, reopens it
// with the same key and reads the data back, then confirms a wrong key cannot
// read it — proving the at-rest encryption passthrough actually encrypts.
func TestOpenEncrypted_RoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "secret.db")
	key := make([]byte, 32) // Adiantum key
	for i := range key {
		key[i] = byte(i + 1)
	}

	db, err := sqlite.OpenEncrypted(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE secrets (id INTEGER PRIMARY KEY, msg TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO secrets (msg) VALUES (?)`, "the eagle lands at dawn"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen with the correct key: data is readable.
	db2, err := sqlite.OpenEncrypted(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var msg string
	if err := one(ctx, db2, &msg, `SELECT msg FROM secrets WHERE id = 1`); err != nil {
		t.Fatalf("read with correct key: %v", err)
	}
	if msg != "the eagle lands at dawn" {
		t.Fatalf("msg = %q, want the plaintext back", msg)
	}

	// Reopen with a wrong key: the file must not decrypt to readable rows.
	wrong := make([]byte, 32)
	for i := range wrong {
		wrong[i] = 0xAA
	}
	db3, err := sqlite.OpenEncrypted(path, wrong)
	if err == nil {
		var leaked string
		if err := one(ctx, db3, &leaked, `SELECT msg FROM secrets WHERE id = 1`); err == nil {
			t.Fatalf("wrong key read plaintext %q — not actually encrypted", leaked)
		}
		db3.Close()
	}
}

// TestOpenEncryptedConfig_AESXTS exercises the full-control entry point with a
// non-default cipher (AES-XTS-256, a 64-byte key): write, reopen with the same
// key/cipher and read back, and confirm a wrong key cannot decrypt.
func TestOpenEncryptedConfig_AESXTS(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aes.db")
	key := make([]byte, 64) // AES-XTS-256 = two 32-byte keys
	for i := range key {
		key[i] = byte(i + 1)
	}
	cfg := func() gosqlite.Config {
		return gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()}
	}

	db, err := sqlite.OpenEncryptedConfig(cfg(), crypto.Options{Key: key, Cipher: crypto.AESXTS})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, msg TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t (msg) VALUES (?)`, "classified"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := sqlite.OpenEncryptedConfig(cfg(), crypto.Options{Key: key, Cipher: crypto.AESXTS})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var msg string
	if err := one(ctx, db2, &msg, `SELECT msg FROM t WHERE id = 1`); err != nil {
		t.Fatalf("read with correct key/cipher: %v", err)
	}
	if msg != "classified" {
		t.Fatalf("msg = %q, want the plaintext back", msg)
	}

	wrong := make([]byte, 64)
	for i := range wrong {
		wrong[i] = 0xAA
	}
	if db3, err := sqlite.OpenEncryptedConfig(cfg(), crypto.Options{Key: wrong, Cipher: crypto.AESXTS}); err == nil {
		var leaked string
		if err := one(ctx, db3, &leaked, `SELECT msg FROM t WHERE id = 1`); err == nil {
			t.Fatalf("wrong key read plaintext %q — not actually encrypted", leaked)
		}
		db3.Close()
	}
}

func one(ctx context.Context, db *liteorm.DB, dst *string, q string) error {
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
