package lob

import (
	"context"
	"fmt"
	"reflect"

	"gosqlite.org/blobstore"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// Info is a backend-neutral summary of a large object's storage, returned by Stat.
type Info struct {
	Size        int64           // logical (uncompressed) length in bytes
	StoredBytes int64           // bytes the object's blocks occupy on disk (Unique+Shared)
	UniqueBytes int64           // bytes in blocks referenced only by this object
	SharedBytes int64           // bytes in blocks shared with clones/versions of it
	Ratio       float64         // StoredBytes/Size (0 when empty; <1 means compressed or sparse)
	ChunkSize   int64           // the object's chunk size
	Compressed  bool            // whether the object is stored compressed
	Level       orm.Compression // compression level used for future writes
}

// Stat reports the storage profile of the row's content object — logical size,
// bytes on disk, compression ratio/level, and the unique/shared split that makes
// a clone's cheapness visible. A field that was never written returns
// ErrNotAllocated.
func Stat[T any](ctx context.Context, sess liteorm.Session, row *T, field string) (Info, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return Info{}, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return Info{}, err
	}
	id := reflect.ValueOf(row).Elem().FieldByIndex(b.lf.Index).Int()
	if id == 0 {
		return Info{}, ErrNotAllocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return Info{}, err
	}
	oi, err := st.Stat(ctx, id)
	if err != nil {
		return Info{}, err
	}
	return Info{
		Size: oi.Size, StoredBytes: oi.StoredBytes, UniqueBytes: oi.UniqueBytes,
		SharedBytes: oi.SharedBytes, Ratio: oi.Ratio, ChunkSize: oi.ChunkSize,
		Compressed: oi.Compressed, Level: fromBlobstoreCompression(oi.Level),
	}, nil
}

// Clone makes dst's content a copy-on-write copy of src's content object (the
// same field on both rows of the same model). It is O(metadata): no bytes are
// copied and the two objects share blocks until one is written, then diverge
// block-by-block. src must be allocated; dst's previous content, if any, is freed
// and replaced by the clone's id (persisted to dst's row and struct).
func Clone[T any](ctx context.Context, sess liteorm.Session, dst, src *T, field string) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return err
	}
	srcID := reflect.ValueOf(src).Elem().FieldByIndex(b.lf.Index).Int()
	if srcID == 0 {
		return ErrNotAllocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return err
	}
	newID, err := st.Clone(ctx, srcID)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(dst).Elem()
	fv := rv.FieldByIndex(b.lf.Index)
	if old := fv.Int(); old != 0 {
		_ = st.Delete(ctx, old) // best-effort: don't orphan dst's prior content
	}
	pkv := rv.FieldByIndex(b.pkIndex).Interface()
	pkRef := string(sess.Dialect().QuoteIdent(nil, b.pkCol))
	if _, err := query.Update[T](sess).Set(b.lf.Column, newID).Where(pkRef+" = ?", pkv).Exec(ctx); err != nil {
		_ = st.Delete(ctx, newID)
		return fmt.Errorf("lob: persist clone id: %w", err)
	}
	fv.SetInt(newID)
	return nil
}

// fromBlobstoreCompression is the inverse of toBlobstoreCompression.
func fromBlobstoreCompression(c blobstore.Compression) orm.Compression {
	switch c {
	case blobstore.CompressionFastest:
		return orm.CompressionFastest
	case blobstore.CompressionFast:
		return orm.CompressionFast
	case blobstore.CompressionDefault:
		return orm.CompressionDefault
	case blobstore.CompressionBetter:
		return orm.CompressionBetter
	case blobstore.CompressionBest:
		return orm.CompressionBest
	default:
		return orm.CompressionNone
	}
}
