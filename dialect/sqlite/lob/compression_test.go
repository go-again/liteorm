package lob_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

// TestLOB_PerObjectCompression proves the per-Open WithCompression override beats
// the field/store default and that raw and compressed objects coexist in one
// store: object A (override=Best) is compressed, object B (no override) is raw.
func TestLOB_PerObjectCompression(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ovdoc](ctx, db); err != nil { // no tag → store default raw
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ovdoc](db)
	body := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 1024)

	a := &ovdoc{}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	wa, err := lob.Open(ctx, db, a, "Content", lob.WithCompression(orm.CompressionBest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wa.WriteAt(body, 0); err != nil {
		t.Fatal(err)
	}
	_ = wa.Close()

	b := &ovdoc{}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	wb, err := lob.Open(ctx, db, b, "Content") // no override → store default (raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.WriteAt(body, 0); err != nil {
		t.Fatal(err)
	}
	_ = wb.Close()

	ga, _ := repo.Get(ctx, a.ID)
	gb, _ := repo.Get(ctx, b.ID)
	codecA := scalarInt(t, db, fmt.Sprintf("SELECT codec FROM ovdocs_content_objects WHERE id=%d", ga.Content.ID()))
	codecB := scalarInt(t, db, fmt.Sprintf("SELECT codec FROM ovdocs_content_objects WHERE id=%d", gb.Content.ID()))
	if codecA == 0 {
		t.Fatal("object A (WithCompression Best) is raw — per-object override didn't apply")
	}
	if codecB != 0 {
		t.Fatalf("object B (no override) codec=%d, want 0 (raw) — they must coexist", codecB)
	}
}

// TestLOB_SetCompression converts an existing raw object to compressed and proves
// content is preserved.
func TestLOB_SetCompression(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ovdoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ovdoc](db)
	d := &ovdoc{}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("compress me later\n"), 2048)
	w, err := lob.Open(ctx, db, d, "Content") // raw
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt(want, 0); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	got, _ := repo.Get(ctx, d.ID)
	id := got.Content.ID()
	if codec := scalarInt(t, db, fmt.Sprintf("SELECT codec FROM ovdocs_content_objects WHERE id=%d", id)); codec != 0 {
		t.Fatalf("object should start raw, codec=%d", codec)
	}
	if err := lob.SetCompression(ctx, db, &got, "Content", orm.CompressionBest); err != nil {
		t.Fatalf("setcompression: %v", err)
	}
	if codec := scalarInt(t, db, fmt.Sprintf("SELECT codec FROM ovdocs_content_objects WHERE id=%d", id)); codec == 0 {
		t.Fatal("SetCompression(Best) did not compress the object")
	}
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(io.NewSectionReader(r, 0, int64(len(want))))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if !bytes.Equal(all, want) {
		t.Fatal("content changed after SetCompression")
	}
}

// TestLOB_Clone proves a clone is a cheap copy-on-write copy: it reads the same
// content as the source, has a distinct object id, and shares storage (Stat
// reports SharedBytes > 0).
func TestLOB_Clone(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ovdoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ovdoc](db)
	src := &ovdoc{}
	if err := repo.Create(ctx, src); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("clone me\n"), 4096)
	w, err := lob.Open(ctx, db, src, "Content", lob.WithCompression(orm.CompressionBest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt(want, 0); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	gsrc, _ := repo.Get(ctx, src.ID)
	dst := &ovdoc{}
	if err := repo.Create(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if err := lob.Clone(ctx, db, dst, &gsrc, "Content"); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !dst.Content.Allocated() || dst.Content.ID() == gsrc.Content.ID() {
		t.Fatalf("clone id = %d, src id = %d; want allocated and distinct", dst.Content.ID(), gsrc.Content.ID())
	}

	r, err := lob.Read(ctx, db, dst, "Content")
	if err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(io.NewSectionReader(r, 0, int64(len(want))))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if !bytes.Equal(all, want) {
		t.Fatal("clone content differs from source")
	}
	info, err := lob.Stat(ctx, db, dst, "Content")
	if err != nil {
		t.Fatal(err)
	}
	if info.SharedBytes == 0 {
		t.Fatalf("clone Stat = %+v; want SharedBytes > 0 (copy-on-write)", info)
	}
}
