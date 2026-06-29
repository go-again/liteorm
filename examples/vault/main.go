// Command vault demonstrates a SQLite database stored in a gosqlite.org/vfs/vault
// container via liteorm's vault subpackage: compressed AND encrypted at rest,
// queried LIVE in place (no plaintext working copy) with per-transaction
// durability. Use the ORM exactly as on a plain database; the on-disk file is a
// compact, ciphertext container.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	gosqlite "gosqlite.org"
	"liteorm.org/dialect/sqlite/vault"
	"liteorm.org/orm"
)

type Note struct {
	ID   int64
	Body string
}

func (Note) TableName() string { return "notes" }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-vault-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "app.db.vault")

	// A 32-byte key (Adiantum). In production this comes from a KMS / secret store.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	const secret = "the eagle lands at dawn — " // compressible AND sensitive
	body := strings.Repeat(secret, 64)          // ~1.6 KB

	// 1. Open compressed + encrypted, live, and use the ORM as on a plain database.
	db, err := vault.OpenConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		vault.Options{Level: vault.CompressionBest, Key: key},
	)
	if err != nil {
		return err
	}
	if err := orm.AutoMigrate[Note](ctx, db); err != nil {
		return err
	}
	notes := orm.NewRepo[Note](db)
	const n = 2000
	for range n {
		if err := notes.Create(ctx, &Note{Body: body}); err != nil {
			return err
		}
	}
	if err := db.Close(); err != nil {
		return err
	}

	// 2. The on-disk container is a fraction of the logical size AND ciphertext.
	logical := int64(len(body)) * n
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte(secret)) {
		return fmt.Errorf("plaintext leaked into the on-disk container")
	}
	fmt.Printf("wrote %d notes — on-disk %s is %d bytes (logical ~%d bytes, %.0fx smaller), no plaintext on disk ✓\n",
		n, filepath.Base(path), len(raw), logical, float64(logical)/float64(len(raw)))

	// 3. Reopen with the key and read it back through the ORM.
	reopened, err := vault.OpenConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		vault.Options{Key: key},
	)
	if err != nil {
		return err
	}
	defer reopened.Close()
	got, err := orm.NewRepo[Note](reopened).Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("reopened the vault → %d notes\n", got)

	// 4. The wrong key cannot open it.
	wrong := make([]byte, 32)
	if _, err := rand.Read(wrong); err != nil {
		return err
	}
	if bad, err := vault.OpenConfig(gosqlite.Config{Path: path}, vault.Options{Key: wrong}); err == nil {
		if _, err := orm.NewRepo[Note](bad).Count(ctx); err == nil {
			_ = bad.Close()
			return fmt.Errorf("the wrong key was able to read the vault")
		}
		_ = bad.Close()
	}
	fmt.Println("the wrong key cannot read the vault ✓")
	return nil
}
