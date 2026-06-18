// Command encryption demonstrates liteorm's at-rest encryption for SQLite (via
// gosqlite's transparent page-level cipher): open the database with a 32-byte key,
// use the ORM exactly as you would unencrypted, and observe that the on-disk file
// is ciphertext — readable only by reopening with the same key. Encryption is an
// open/connection concern, orthogonal to what you do with the data afterward.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

type Note struct {
	ID   int64
	Text string
}

func (Note) TableName() string { return "notes" }

const secret = "the launch codes are 0000"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-encryption-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "secret.db")

	// A 32-byte key. In production this comes from a KMS / secret store, never a
	// literal or a value you can lose — losing the key means losing the data.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}

	// 1. Open encrypted and use the ORM exactly as you would on a plain database.
	db, err := sqlite.OpenEncrypted(path, key)
	if err != nil {
		return err
	}
	if err := orm.AutoMigrate[Note](ctx, db); err != nil {
		return err
	}
	notes := orm.NewRepo[Note](db)
	n := &Note{Text: secret}
	if err := notes.Create(ctx, n); err != nil {
		return err
	}
	_ = db.Close()
	fmt.Printf("wrote %s with an encryption key\n", filepath.Base(path))

	// 2. The raw file is ciphertext — the plaintext must not appear on disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte(secret)) {
		return fmt.Errorf("plaintext leaked into the on-disk file")
	}
	fmt.Println("on-disk bytes do NOT contain the plaintext — encrypted at rest ✓")

	// 3. Reopen with the SAME key and read it back through the ORM.
	reopened, err := sqlite.OpenEncrypted(path, key)
	if err != nil {
		return err
	}
	defer reopened.Close()
	got, err := orm.NewRepo[Note](reopened).Get(ctx, n.ID)
	if err != nil {
		return err
	}
	fmt.Printf("reopened with the key → %q\n", got.Text)

	// 4. The WRONG key cannot read it (decryption fails).
	wrong := make([]byte, 32)
	if _, err := rand.Read(wrong); err != nil {
		return err
	}
	switch bad, err := sqlite.OpenEncrypted(path, wrong); {
	case err != nil:
		fmt.Println("opening with the wrong key fails ✓")
	default:
		_, err = orm.NewRepo[Note](bad).Get(ctx, n.ID)
		_ = bad.Close()
		if err == nil {
			return fmt.Errorf("the wrong key was able to read the data")
		}
		fmt.Println("the wrong key cannot read the data ✓")
	}
	return nil
}
