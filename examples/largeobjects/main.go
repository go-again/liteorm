// Command largeobjects demonstrates LiteORM's streamed large-object storage: a
// model field of type orm.LOB keeps binary content in SQLite that is written and
// read incrementally as io.WriterAt/io.ReaderAt, never loaded whole. The row
// carries only an 8-byte object id; the bytes live in a content sidecar that
// AutoMigrate provisions when liteorm.org/dialect/sqlite/lob is imported.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

// File stores metadata as ordinary columns and its bytes as a large object.
type File struct {
	ID      int64   `orm:"id,pk"`
	Path    string  `orm:"path,unique"`
	Content orm.LOB `lob:"chunk=64k"` // streamed content; only the id lives on the row
}

func (File) TableName() string { return "files" }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "liteorm-lob")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "files.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	// AutoMigrate creates `files` and provisions the `files_content` object store.
	if err := orm.AutoMigrate[File](ctx, db); err != nil {
		return err
	}
	repo := orm.NewRepo[File](db)

	f := &File{Path: "/reports/q3.csv"}
	if err := repo.Create(ctx, f); err != nil {
		return err
	}
	fmt.Printf("created %s (content allocated: %v)\n", f.Path, f.Content.Allocated())

	// Stream ~1 MiB of content in. In a real app `src` is an upload/io.Reader;
	// nothing is buffered whole in memory.
	src := strings.NewReader(strings.Repeat("id,name,amount\n", 75_000))
	w, err := lob.Open(ctx, db, f, "Content")
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.NewOffsetWriter(w, 0), src); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	size, err := lob.Size(ctx, db, f, "Content")
	if err != nil {
		return err
	}
	fmt.Printf("stored %d bytes as object id %d\n", size, f.Content.ID())

	// The id was persisted: a fresh load from the DB sees the same object.
	got, err := repo.Get(ctx, f.ID)
	if err != nil {
		return err
	}
	fmt.Printf("reloaded row: content object id %d\n", got.Content.ID())

	// Read a 60-byte range from the middle — no whole-object load.
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		return err
	}
	mid, err := io.ReadAll(io.NewSectionReader(r, size/2, 60))
	_ = r.Close()
	if err != nil {
		return err
	}
	fmt.Printf("range at %d: %q\n", size/2, mid)

	// Checksum by streaming the whole object in O(chunk) memory.
	r2, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r2, 0, size)); err != nil {
		return err
	}
	_ = r2.Close()
	fmt.Printf("sha256: %x\n", h.Sum(nil))

	// Free the content when the file is removed.
	if err := lob.Drop(ctx, db, &got, "Content"); err != nil {
		return err
	}
	fmt.Printf("dropped content (allocated: %v)\n", got.Content.Allocated())
	return nil
}
