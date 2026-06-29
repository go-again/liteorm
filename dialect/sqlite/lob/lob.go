// Package lob streams large-object content for liteorm models whose fields are
// orm.LOB, backed by the sibling driver's gosqlite.org/blobstore engine. The
// model row stores only an object id (an INTEGER column); the bytes are read and
// written out-of-band as io.ReaderAt/io.WriterAt, never materialized with the row.
//
// It is SQLite-specific: every helper takes a session opened by
// liteorm.org/dialect/sqlite and returns ErrUnsupportedBackend for any other
// dialect. Importing this package registers the AutoMigrate provisioner for
// orm.LOB fields, so a model with a LOB field provisions its content store on
// AutoMigrate just as a search index provisions its sidecar.
package lob

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	gosqlite "gosqlite.org"
	"gosqlite.org/blobstore"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// ErrUnsupportedBackend is returned when the session is not a SQLite database
// opened by dialect/sqlite (large objects are a SQLite-only feature).
var ErrUnsupportedBackend = errors.New("liteorm/lob: session is not a SQLite database opened by dialect/sqlite")

// ErrNotAllocated is returned by Read/Size for a LOB field that has never been
// written, so no content object exists yet. Treat it as empty content.
var ErrNotAllocated = errors.New("liteorm/lob: field has no content object yet")

func init() { orm.RegisterLOBProvisioner(provision) }

// provision creates (idempotently) the object store backing one LOB column. It is
// the AutoMigrate hook registered with orm; applications do not call it.
func provision(ctx context.Context, sess liteorm.Session, store string, opts orm.LOBProvisionOptions) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	_, err := storeFor(g, store, opts.ChunkSize, opts.Compression, opts.Dedup)
	return err
}

// storeCache memoizes opened Stores per (database, name), so repeated Open/Read
// calls don't re-run blobstore's idempotent CREATE TABLE on every access. A Store
// is safe for concurrent use and doesn't own the db, so caching it is sound.
var storeCache sync.Map // map[cacheKey]*blobstore.Store

type cacheKey struct {
	db   *gosqlite.DB
	name string
}

// storeFor returns the (cached) Store for a database + name, opening it (which
// idempotently creates the backing tables) on first use. chunkSize and
// compression come from the column's resolved orm.LOBField; they configure the
// Store that allocates new objects (both are frozen per object at Create), so a
// Writer that may allocate must open with them. A store name maps to exactly one
// column, so caching by (db, name) is sound.
func storeFor(g *gosqlite.DB, name string, chunkSize int, compression orm.Compression, dedup bool) (*blobstore.Store, error) {
	key := cacheKey{db: g, name: name}
	if v, ok := storeCache.Load(key); ok {
		return v.(*blobstore.Store), nil
	}
	var opts []blobstore.Option
	if chunkSize > 0 {
		opts = append(opts, blobstore.WithChunkSize(chunkSize))
	}
	if compression != orm.CompressionNone {
		opts = append(opts, blobstore.WithCompression(toBlobstoreCompression(compression)))
	}
	if dedup {
		opts = append(opts, blobstore.WithDedup())
	}
	st, err := blobstore.Open(g, name, opts...)
	if err != nil {
		return nil, err
	}
	actual, _ := storeCache.LoadOrStore(key, st)
	return actual.(*blobstore.Store), nil
}

// storeForField is storeFor with the options taken from a resolved field binding.
func storeForField(g *gosqlite.DB, b binding) (*blobstore.Store, error) {
	return storeFor(g, b.store, b.lf.ChunkSize, b.lf.Compression, b.lf.Dedup)
}

// toBlobstoreCompression maps the backend-neutral orm.Compression onto the
// blobstore engine's level. The orderings match, but the switch is explicit so a
// future divergence on either side fails to compile rather than mis-mapping.
func toBlobstoreCompression(c orm.Compression) blobstore.Compression {
	switch c {
	case orm.CompressionFastest:
		return blobstore.CompressionFastest
	case orm.CompressionFast:
		return blobstore.CompressionFast
	case orm.CompressionDefault:
		return blobstore.CompressionDefault
	case orm.CompressionBetter:
		return blobstore.CompressionBetter
	case orm.CompressionBest:
		return blobstore.CompressionBest
	default:
		return blobstore.CompressionNone
	}
}

// binding is the resolved mapping from a model's LOB field to its store and the
// row's primary key (used to persist a newly allocated object id back).
type binding struct {
	store   string
	lf      orm.LOBField
	pkCol   string
	pkIndex []int
}

func bind[T any](field string) (binding, error) {
	s, err := orm.SchemaOf[T]()
	if err != nil {
		return binding{}, err
	}
	var lf orm.LOBField
	found := false
	for _, x := range s.LOBFields {
		if x.GoName == field {
			lf, found = x, true
			break
		}
	}
	if !found {
		return binding{}, fmt.Errorf("lob: %s has no orm.LOB field %q", s.Table, field)
	}
	if s.PK == nil {
		return binding{}, fmt.Errorf("lob: %s needs a single primary key to address large objects", s.Table)
	}
	return binding{store: orm.LOBStoreName(s.Table, lf.Column), lf: lf, pkCol: s.PK.Column, pkIndex: s.PK.Index}, nil
}

// creator is the minimal allocation surface ensure needs, satisfied by both a
// pooled *blobstore.Store and a transaction-joining *blobstore.ConnStore.
type creator interface {
	Create(ctx context.Context, opts ...blobstore.CreateOption) (int64, error)
	Delete(ctx context.Context, id int64) error
}

// ensure returns the object id for the field, allocating (and persisting back to
// the row) one on first access. The id-writeback runs through sess, so when sess
// is a transaction-bound session the writeback joins that transaction.
func ensure[T any](ctx context.Context, sess liteorm.Session, c creator, rv reflect.Value, b binding, createOpts ...blobstore.CreateOption) (int64, error) {
	fv := rv.FieldByIndex(b.lf.Index)
	if id := fv.Int(); id != 0 {
		return id, nil
	}
	id, err := c.Create(ctx, createOpts...)
	if err != nil {
		return 0, err
	}
	pkv := rv.FieldByIndex(b.pkIndex).Interface()
	pkRef := string(sess.Dialect().QuoteIdent(nil, b.pkCol))
	if _, err := query.Update[T](sess).Set(b.lf.Column, id).Where(pkRef+" = ?", pkv).Exec(ctx); err != nil {
		_ = c.Delete(ctx, id) // best-effort: don't leak the object we just created
		return 0, fmt.Errorf("lob: persist object id: %w", err)
	}
	fv.SetInt(id)
	return id, nil
}

// Open returns an io.WriterAt+io.Closer over the row's large-object content,
// allocating the backing object (and writing its id back into row) on first use.
// Offsets may be written in any order; gaps are sparse. A WithCompression option
// sets the at-rest compression of the object allocated on first use, overriding
// the field's tag default for that object only.
func Open[T any](ctx context.Context, sess liteorm.Session, row *T, field string, opts ...Option) (*blobstore.Writer, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return nil, err
	}
	st, err := storeForField(g, b)
	if err != nil {
		return nil, err
	}
	id, err := ensure[T](ctx, sess, st, reflect.ValueOf(row).Elem(), b, collect(opts).createOpts()...)
	if err != nil {
		return nil, err
	}
	return st.Writer(ctx, id)
}

// Read returns an io.ReaderAt+io.Closer over the row's large-object content.
// A field that has never been written returns ErrNotAllocated (treat as empty).
func Read[T any](ctx context.Context, sess liteorm.Session, row *T, field string) (*blobstore.Reader, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return nil, err
	}
	id := reflect.ValueOf(row).Elem().FieldByIndex(b.lf.Index).Int()
	if id == 0 {
		return nil, ErrNotAllocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return nil, err
	}
	return st.Reader(ctx, id)
}

// Size reports the logical byte length of the row's content (0 if never written).
func Size[T any](ctx context.Context, sess liteorm.Session, row *T, field string) (int64, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return 0, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return 0, err
	}
	id := reflect.ValueOf(row).Elem().FieldByIndex(b.lf.Index).Int()
	if id == 0 {
		return 0, nil
	}
	st, err := storeForField(g, b)
	if err != nil {
		return 0, err
	}
	return st.Size(ctx, id)
}

// Truncate sets the row's content to exactly size bytes (growing sparsely or
// shrinking), allocating the object first if needed. A WithCompression option
// sets the at-rest compression of the object if this call is what allocates it.
func Truncate[T any](ctx context.Context, sess liteorm.Session, row *T, field string, size int64, opts ...Option) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(row).Elem()
	if size == 0 && rv.FieldByIndex(b.lf.Index).Int() == 0 {
		return nil // already empty + unallocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return err
	}
	id, err := ensure[T](ctx, sess, st, rv, b, collect(opts).createOpts()...)
	if err != nil {
		return err
	}
	return st.Truncate(ctx, id, size)
}

// Drop frees the row's content object and resets the field to unallocated. It is
// a no-op for a field that was never written. Call it when hard-deleting a row
// (e.g. from a BeforeDelete hook) so the content does not outlive its owner.
func Drop[T any](ctx context.Context, sess liteorm.Session, row *T, field string) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(row).Elem()
	fv := rv.FieldByIndex(b.lf.Index)
	id := fv.Int()
	if id == 0 {
		return nil
	}
	st, err := storeForField(g, b)
	if err != nil {
		return err
	}
	if err := st.Delete(ctx, id); err != nil && !errors.Is(err, blobstore.ErrNotFound) {
		return err
	}
	pkv := rv.FieldByIndex(b.pkIndex).Interface()
	pkRef := string(sess.Dialect().QuoteIdent(nil, b.pkCol))
	if _, err := query.Update[T](sess).Set(b.lf.Column, 0).Where(pkRef+" = ?", pkv).Exec(ctx); err != nil {
		return fmt.Errorf("lob: clear object id: %w", err)
	}
	fv.SetInt(0)
	return nil
}
