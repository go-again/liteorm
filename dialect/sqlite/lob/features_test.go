package lob_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

// TestLOB_WriteFrom streams content from an io.Reader and reads it back.
func TestLOB_WriteFrom(t *testing.T) {
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
	want := bytes.Repeat([]byte("streamed\n"), 5000)
	n, err := lob.WriteFrom(ctx, db, d, "Content", bytes.NewReader(want), lob.WithCompression(orm.CompressionBest))
	if err != nil {
		t.Fatalf("writefrom: %v", err)
	}
	if n != int64(len(want)) {
		t.Fatalf("wrote %d bytes, want %d", n, len(want))
	}
	got, _ := repo.Get(ctx, d.ID)
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := io.ReadAll(io.NewSectionReader(r, 0, n))
	_ = r.Close()
	if !bytes.Equal(all, want) {
		t.Fatal("WriteFrom content mismatch")
	}
}

type ddoc struct {
	ID      int64   `orm:"id,pk"`
	Content orm.LOB `lob:"dedup;compress=default"`
}

func (ddoc) TableName() string { return "ddocs" }

// TestLOB_Dedup proves a dedup store shares blocks across objects with identical
// content: a second object written with the same bytes reports SharedBytes > 0.
func TestLOB_Dedup(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ddoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ddoc](db)
	content := bytes.Repeat([]byte("identical block payload aaaa\n"), 4096)

	write := func() *ddoc {
		d := &ddoc{}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		w, err := lob.Open(ctx, db, d, "Content")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.WriteAt(content, 0); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		got, _ := repo.Get(ctx, d.ID)
		return &got
	}
	_ = write()  // object A
	b := write() // object B — identical content

	info, err := lob.Stat(ctx, db, b, "Content")
	if err != nil {
		t.Fatal(err)
	}
	if info.SharedBytes == 0 {
		t.Fatalf("dedup Stat = %+v; want SharedBytes > 0 (B should share A's blocks)", info)
	}
}

// TestLOB_Versioning snapshots a version, overwrites the live content, then reads
// both the old version and the new live content back.
func TestLOB_Versioning(t *testing.T) {
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
	v1 := bytes.Repeat([]byte("version-one\n"), 100)
	if _, err := lob.WriteFrom(ctx, db, d, "Content", bytes.NewReader(v1), lob.WithVersioning(lob.Policy{KeepVersions: 5})); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	got, _ := repo.Get(ctx, d.ID)

	vn, err := lob.NewVersion(ctx, db, &got, "Content", lob.WithLabel("first"))
	if err != nil {
		t.Fatalf("newversion: %v", err)
	}
	if vn != 1 {
		t.Fatalf("version no = %d, want 1", vn)
	}

	// Overwrite the live content with v2 (truncate first so it fully replaces).
	if err := lob.Truncate(ctx, db, &got, "Content", 0); err != nil {
		t.Fatal(err)
	}
	v2 := bytes.Repeat([]byte("version-TWO\n"), 100)
	if _, err := lob.WriteFrom(ctx, db, &got, "Content", bytes.NewReader(v2)); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	vs, err := lob.ListVersions(ctx, db, &got, "Content")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].VersionNo != 1 || vs[0].Label != "first" || vs[0].Size != int64(len(v1)) {
		t.Fatalf("ListVersions = %+v; want one v1/first/size=%d", vs, len(v1))
	}

	// The saved version reads back v1; the live content reads back v2.
	rv, err := lob.OpenVersion(ctx, db, &got, "Content", 1)
	if err != nil {
		t.Fatal(err)
	}
	oldContent, _ := io.ReadAll(io.NewSectionReader(rv, 0, int64(len(v1))))
	_ = rv.Close()
	if !bytes.Equal(oldContent, v1) {
		t.Fatal("OpenVersion(1) did not return v1 content")
	}
	rc, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := io.ReadAll(io.NewSectionReader(rc, 0, int64(len(v2))))
	_ = rc.Close()
	if !bytes.Equal(live, v2) {
		t.Fatal("live Read did not return v2 content")
	}
}
