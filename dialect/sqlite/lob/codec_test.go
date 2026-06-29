package lob_test

import (
	"bytes"
	"testing"

	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

func TestLOB_CompressDecompress(t *testing.T) {
	want := bytes.Repeat([]byte("compress this small value\n"), 500)
	c, err := lob.Compress(want, orm.CompressionBest)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) >= len(want) {
		t.Fatalf("compressed %d bytes not smaller than %d", len(c), len(want))
	}
	back, err := lob.Decompress(c, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, want) {
		t.Fatal("Compress/Decompress round-trip mismatch")
	}
	if got, _ := lob.Decompress(nil, 0); got != nil {
		t.Fatalf("Decompress(nil) = %v, want nil", got)
	}
	// A non-positive maxSize means "no cap" — it must round-trip real data, not
	// reject it (the underlying decoder rejects a literal max==0).
	if back0, err := lob.Decompress(c, 0); err != nil || !bytes.Equal(back0, want) {
		t.Fatalf("Decompress(c, 0) = %d bytes, %v; want full round-trip (no cap)", len(back0), err)
	}
}
