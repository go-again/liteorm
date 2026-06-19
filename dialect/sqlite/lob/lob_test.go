package lob_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

type doc struct {
	ID      int64 `orm:"id,pk"`
	Name    string
	Content orm.LOB `lob:"chunk=16"` // tiny chunks so a 40-byte write spans 3
}

func (doc) TableName() string { return "docs" }

func openDB(t *testing.T) *liteorm.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "lob.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func scalarInt(t *testing.T, db *liteorm.DB, q string) int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	return n
}

func TestLOB_EndToEnd(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[doc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// AutoMigrate provisioned the content store (importing lob registered it).
	if n := scalarInt(t, db, "SELECT count(*) FROM sqlite_master WHERE name='docs_content_objects'"); n != 1 {
		t.Fatal("docs_content_objects not provisioned by AutoMigrate")
	}

	repo := orm.NewRepo[doc](db)
	d := &doc{Name: "a"}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	if d.Content.Allocated() {
		t.Fatal("Content should be unallocated before first write")
	}

	// Write 40 bytes out of order (second half first), spanning 3 chunks.
	want := bytes.Repeat([]byte("xyz0"), 10) // 40 bytes
	w, err := lob.Open(ctx, db, d, "Content")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.WriteAt(want[20:], 20); err != nil {
		t.Fatalf("writeat tail: %v", err)
	}
	if _, err := w.WriteAt(want[:20], 0); err != nil {
		t.Fatalf("writeat head: %v", err)
	}
	_ = w.Close()
	if !d.Content.Allocated() {
		t.Fatal("Content id should be written back into the row after first write")
	}

	// The id was persisted to the DB, not only the in-memory struct.
	got, err := repo.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content.ID() != d.Content.ID() || !got.Content.Allocated() {
		t.Fatalf("reloaded Content = %d, want %d", got.Content.ID(), d.Content.ID())
	}

	// Read it back.
	if sz, _ := lob.Size(ctx, db, &got, "Content"); sz != 40 {
		t.Fatalf("size = %d, want 40", sz)
	}
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	all, err := io.ReadAll(io.NewSectionReader(r, 0, 40))
	if err != nil {
		t.Fatalf("readall: %v", err)
	}
	_ = r.Close()
	if !bytes.Equal(all, want) {
		t.Fatalf("read %q, want %q", all, want)
	}

	// Truncate (shrink), then Drop frees and resets.
	if err := lob.Truncate(ctx, db, &got, "Content", 10); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if sz, _ := lob.Size(ctx, db, &got, "Content"); sz != 10 {
		t.Fatalf("size after truncate = %d, want 10", sz)
	}
	if err := lob.Drop(ctx, db, &got, "Content"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got.Content.Allocated() {
		t.Fatal("Content should be unallocated after Drop")
	}
}

type cdoc struct {
	ID      int64   `orm:"id,pk"`
	Content orm.LOB `lob:"chunk=4k;compress=default"` // compressed, multi-chunk
}

func (cdoc) TableName() string { return "cdocs" }

// TestLOB_Compressed proves a compress=... model round-trips byte-for-byte and
// reports the logical (uncompressed) size — content is transparently compressed
// at rest and the streaming surface is mode-agnostic.
func TestLOB_Compressed(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[cdoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[cdoc](db)
	d := &cdoc{}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}

	// 20 KiB of highly compressible content spanning several 4 KiB chunks.
	want := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 466)[:20<<10]
	w, err := lob.Open(ctx, db, d, "Content")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.WriteAt(want, 0); err != nil {
		t.Fatalf("writeat: %v", err)
	}
	_ = w.Close()

	got, err := repo.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sz, _ := lob.Size(ctx, db, &got, "Content"); sz != int64(len(want)) {
		t.Fatalf("size = %d, want %d (logical, not compressed)", sz, len(want))
	}
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	all, err := io.ReadAll(io.NewSectionReader(r, 0, int64(len(want))))
	if err != nil {
		t.Fatalf("readall: %v", err)
	}
	_ = r.Close()
	if !bytes.Equal(all, want) {
		t.Fatalf("compressed round-trip mismatch: got %d bytes, want %d", len(all), len(want))
	}

	// Prove compression actually engaged — a round-trip alone would pass even if
	// the codec were a silent no-op. The object's frozen codec must be non-raw,
	// and the physical bytes stored across its chunks must be smaller than the
	// logical length for this highly-compressible payload.
	id := got.Content.ID()
	if codec := scalarInt(t, db, fmt.Sprintf("SELECT codec FROM cdocs_content_objects WHERE id=%d", id)); codec == 0 {
		t.Fatalf("object codec = 0 (raw); compression did not engage")
	}
	stored := scalarInt(t, db, fmt.Sprintf("SELECT COALESCE(SUM(LENGTH(data)),0) FROM cdocs_content_chunks WHERE obj=%d", id))
	if stored == 0 || stored >= int64(len(want)) {
		t.Fatalf("stored bytes = %d, want >0 and < logical %d (content was not compressed)", stored, len(want))
	}
}

func TestLOB_ReadUnallocated(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[doc](ctx, db); err != nil {
		t.Fatal(err)
	}
	d := &doc{Name: "empty"}
	if err := orm.NewRepo[doc](db).Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := lob.Read(ctx, db, d, "Content"); !errors.Is(err, lob.ErrNotAllocated) {
		t.Fatalf("read unallocated = %v, want ErrNotAllocated", err)
	}
	if sz, _ := lob.Size(ctx, db, d, "Content"); sz != 0 {
		t.Errorf("size of unallocated = %d, want 0", sz)
	}
}
