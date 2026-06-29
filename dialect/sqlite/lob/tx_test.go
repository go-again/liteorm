package lob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

// TestLOB_InTx_Atomic is the proof that a row write and its large-object content
// written through InTx commit or roll back together: a returned error leaves
// neither the row nor the content object behind, and success persists both.
func TestLOB_InTx_Atomic(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ovdoc](ctx, db); err != nil { // provisions the content store
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ovdoc](db)
	content := bytes.Repeat([]byte("atomic-content\n"), 1000)

	// 1. Error inside InTx → the row AND the content object roll back.
	boom := errors.New("boom")
	err := lob.InTx(ctx, db, func(tx *lob.Tx) error {
		d := &ovdoc{}
		if err := orm.NewRepo[ovdoc](tx.Session()).Create(ctx, d); err != nil {
			return err
		}
		w, err := lob.OpenTx(ctx, tx, d, "Content")
		if err != nil {
			return err
		}
		if _, err := w.WriteAt(content, 0); err != nil {
			return err
		}
		return boom // force rollback
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx err = %v, want boom", err)
	}
	if n, _ := repo.Count(ctx); n != 0 {
		t.Fatalf("rolled-back row persisted: %d rows", n)
	}
	if n := scalarInt(t, db, "SELECT count(*) FROM ovdocs_content_objects"); n != 0 {
		t.Fatalf("rolled-back content persisted: %d objects", n)
	}

	// 2. Success → the row AND the content commit and read back.
	var id int64
	if err := lob.InTx(ctx, db, func(tx *lob.Tx) error {
		d := &ovdoc{}
		if err := orm.NewRepo[ovdoc](tx.Session()).Create(ctx, d); err != nil {
			return err
		}
		w, err := lob.OpenTx(ctx, tx, d, "Content", lob.WithCompression(orm.CompressionBest))
		if err != nil {
			return err
		}
		if _, err := w.WriteAt(content, 0); err != nil {
			return err
		}
		id = d.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("committed row missing: %v", err)
	}
	if !got.Content.Allocated() {
		t.Fatal("committed content id not persisted to the row")
	}
	r, err := lob.Read(ctx, db, &got, "Content")
	if err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(io.NewSectionReader(r, 0, int64(len(content))))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if !bytes.Equal(all, content) {
		t.Fatal("committed content mismatch")
	}
}

// TestLOB_InTx_DropAtomic confirms DropTx deletes a row and its content together,
// and rolls both back on error.
func TestLOB_InTx_DropAtomic(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ovdoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[ovdoc](db)

	// Seed a row with content (committed, outside any InTx).
	d := &ovdoc{}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	w, err := lob.Open(ctx, db, d, "Content")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt([]byte("bye"), 0); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	// Drop the content in a rolled-back InTx → the object stays.
	boom := errors.New("boom")
	_ = lob.InTx(ctx, db, func(tx *lob.Tx) error {
		got, _ := repo.Get(ctx, d.ID)
		if err := lob.DropTx(ctx, tx, &got, "Content"); err != nil {
			return err
		}
		return boom
	})
	if n := scalarInt(t, db, "SELECT count(*) FROM ovdocs_content_objects"); n != 1 {
		t.Fatalf("after rolled-back DropTx, objects = %d, want 1 (kept)", n)
	}

	// Drop in a committed InTx → the object is gone.
	if err := lob.InTx(ctx, db, func(tx *lob.Tx) error {
		got, _ := repo.Get(ctx, d.ID)
		return lob.DropTx(ctx, tx, &got, "Content")
	}); err != nil {
		t.Fatal(err)
	}
	if n := scalarInt(t, db, "SELECT count(*) FROM ovdocs_content_objects"); n != 0 {
		t.Fatalf("after committed DropTx, objects = %d, want 0", n)
	}
}
