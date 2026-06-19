// Command compressed demonstrates a SQLite database stored compressed on disk
// via liteorm's OpenCompressed (backed by gosqlite.org/vfs/compress): use the
// ORM exactly as on a plain database, and the on-disk file is a compact archive.
//
// Snapshot model — read this. While open, the database runs from a full,
// uncompressed working copy in the OS temp dir; the compressed file is rewritten
// only on Close. So durability is per-session, not per-transaction (a crash while
// open reverts to the last Close), and the working copy is plaintext (not a
// substitute for at-rest encryption). It fits archival, distribution, and
// open-modify-close tooling over compressible data.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	gosqlite "gosqlite.org"
	"gosqlite.org/vfs/compress"
	"liteorm.org/dialect/sqlite"
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
	dir, err := os.MkdirTemp("", "liteorm-compressed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "app.db.az")

	// 1. Open compressed at the best level and use the ORM as on a plain database.
	db, err := sqlite.OpenCompressedConfig(
		gosqlite.Config{Path: path, Pragmas: gosqlite.RecommendedPragmas()},
		compress.Options{Level: compress.CompressionBest},
	)
	if err != nil {
		return err
	}
	if err := orm.AutoMigrate[Note](ctx, db); err != nil {
		return err
	}
	notes := orm.NewRepo[Note](db)
	body := strings.Repeat("the quick brown fox jumps over the lazy dog ", 64) // ~2.8 KB, compressible
	const n = 2000
	for range n {
		if err := notes.Create(ctx, &Note{Body: body}); err != nil {
			return err
		}
	}
	// Close compresses the working copy over the path — this is the durable point.
	if err := db.Close(); err != nil {
		return err
	}

	// 2. The on-disk file is a fraction of the logical content.
	logical := int64(len(body)) * n
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %d notes — on-disk %s is %d bytes (logical ~%d bytes, %.0fx smaller)\n",
		n, filepath.Base(path), info.Size(), logical, float64(logical)/float64(info.Size()))

	// 3. Reopen (the level is auto-detected) and read it back through the ORM.
	reopened, err := sqlite.OpenCompressed(path)
	if err != nil {
		return err
	}
	defer reopened.Close()
	got, err := orm.NewRepo[Note](reopened).Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("reopened the compressed database → %d notes\n", got)
	return nil
}
