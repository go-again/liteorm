package lob

import (
	"context"
	"reflect"
	"time"

	"gosqlite.org/blobstore"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// Policy is a large object's version-retention policy.
type Policy struct {
	KeepVersions int           // keep only the N most recent versions (0 = unlimited)
	MaxAge       time.Duration // delete versions older than this (0 = no age bound)
}

// VersionInfo describes one saved version of a large object.
type VersionInfo struct {
	VersionNo int64     // monotonically increasing per object, starting at 1
	CreatedAt time.Time // when the version was taken
	Label     string    // optional caller label ("" if none)
	Size      int64     // logical size at the time of the snapshot
}

// VersionOption configures NewVersion (currently a label).
type VersionOption func(*versionOpts)

type versionOpts struct{ label string }

// WithLabel attaches a label to the version created by NewVersion.
func WithLabel(label string) VersionOption {
	return func(o *versionOpts) { o.label = label }
}

func toBlobstorePolicy(p Policy) blobstore.Policy {
	return blobstore.Policy{KeepVersions: p.KeepVersions, MaxAge: p.MaxAge}
}

// objectStore resolves the (pooled) store + object id for a row's field, requiring
// the field to be allocated. Shared by the version helpers.
func objectStore[T any](sess liteorm.Session, row *T, field string) (*blobstore.Store, int64, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, 0, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return nil, 0, err
	}
	id := reflect.ValueOf(row).Elem().FieldByIndex(b.lf.Index).Int()
	if id == 0 {
		return nil, 0, ErrNotAllocated
	}
	st, err := storeForField(g, b)
	if err != nil {
		return nil, 0, err
	}
	return st, id, nil
}

// NewVersion snapshots the row's current content as a new immutable version and
// returns its version number. The live content continues to evolve from there;
// retention (see SetRetention or WithVersioning) may prune old versions.
func NewVersion[T any](ctx context.Context, sess liteorm.Session, row *T, field string, opts ...VersionOption) (int64, error) {
	st, id, err := objectStore(sess, row, field)
	if err != nil {
		return 0, err
	}
	var vo versionOpts
	for _, fn := range opts {
		fn(&vo)
	}
	var bopts []blobstore.VersionOption
	if vo.label != "" {
		bopts = append(bopts, blobstore.WithLabel(vo.label))
	}
	return st.NewVersion(ctx, id, bopts...)
}

// ListVersions returns the row's saved versions, oldest first.
func ListVersions[T any](ctx context.Context, sess liteorm.Session, row *T, field string) ([]VersionInfo, error) {
	st, id, err := objectStore(sess, row, field)
	if err != nil {
		return nil, err
	}
	vs, err := st.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]VersionInfo, len(vs))
	for i, v := range vs {
		out[i] = VersionInfo{VersionNo: v.VersionNo, CreatedAt: v.CreatedAt, Label: v.Label, Size: v.Size}
	}
	return out, nil
}

// OpenVersion returns an io.ReaderAt+io.Closer over a past version's content.
func OpenVersion[T any](ctx context.Context, sess liteorm.Session, row *T, field string, versionNo int64) (*blobstore.Reader, error) {
	st, id, err := objectStore(sess, row, field)
	if err != nil {
		return nil, err
	}
	return st.OpenVersion(ctx, id, versionNo)
}

// SetRetention sets the row object's version-retention policy and prunes
// immediately to enforce it.
func SetRetention[T any](ctx context.Context, sess liteorm.Session, row *T, field string, p Policy) error {
	st, id, err := objectStore(sess, row, field)
	if err != nil {
		return err
	}
	return st.SetRetention(ctx, id, toBlobstorePolicy(p))
}

// Prune deletes versions that fall outside the row object's retention policy now.
func Prune[T any](ctx context.Context, sess liteorm.Session, row *T, field string) error {
	st, id, err := objectStore(sess, row, field)
	if err != nil {
		return err
	}
	return st.Prune(ctx, id)
}
