package lob

import (
	"context"
	"reflect"

	"gosqlite.org/blobstore"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// Option configures a per-object large-object operation. It currently sets the
// at-rest compression level of the content object an Open or Truncate allocates,
// overriding the model's lob:"compress=…" tag default for that one object — raw
// and compressed objects coexist in the same store.
type Option func(*objOptions)

type objOptions struct {
	compression    orm.Compression
	hasCompression bool
	versioning     *Policy
}

// WithCompression sets the compression level for the content object allocated by
// this Open/Truncate call, overriding the field's tag default. It applies only
// when the object is first created; on an already-allocated field it is ignored
// (use SetCompression to change an existing object's level).
func WithCompression(c orm.Compression) Option {
	return func(o *objOptions) { o.compression, o.hasCompression = c, true }
}

// WithVersioning sets the version-retention policy for the content object
// allocated by this Open/Truncate call (see NewVersion). Applies only on first
// creation; change an existing object's policy with SetRetention.
func WithVersioning(p Policy) Option {
	return func(o *objOptions) { o.versioning = &p }
}

func collect(opts []Option) objOptions {
	var o objOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func (o objOptions) createOpts() []blobstore.CreateOption {
	var out []blobstore.CreateOption
	if o.hasCompression {
		out = append(out, blobstore.WithObjectCompression(toBlobstoreCompression(o.compression)))
	}
	if o.versioning != nil {
		out = append(out, blobstore.WithObjectVersioning(toBlobstorePolicy(*o.versioning)))
	}
	return out
}

// SetCompression changes the at-rest compression level of the row's existing
// content object. A level-only change on an already-compressed object rewrites
// nothing (reads are level-agnostic); switching between raw and compressed
// rewrites the object's chunks in one transaction, content preserved. The field
// must be allocated (write something first) — ErrNotAllocated otherwise.
func SetCompression[T any](ctx context.Context, sess liteorm.Session, row *T, field string, c orm.Compression) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return err
	}
	id := reflect.ValueOf(row).Elem().FieldByIndex(b.lf.Index).Int()
	if id == 0 {
		return ErrNotAllocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return err
	}
	return st.SetCompression(ctx, id, toBlobstoreCompression(c))
}
