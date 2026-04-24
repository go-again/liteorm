// Package changeset exposes SQLite's SESSION extension (changesets/patchsets)
// through liteorm's SQLite backend, for audit logs and one-way replication. A
// changeset is a compact binary diff of the rows a set of statements touched; it
// can be captured on one database, inspected, inverted, and applied to another
// with a Go conflict handler.
//
// It is SQLite-specific and capability-gated: Capture/Apply take a liteorm
// session opened by liteorm.org/dialect/sqlite. Capture pins a dedicated
// connection so the recording session and the mutations share one physical
// connection — the mutations passed to Capture MUST run against the session it
// hands back, not the original handle.
package changeset

import (
	"context"

	gosqlite "gosqlite.org"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// ConflictType describes why applying a change conflicted with the target.
type ConflictType = gosqlite.ConflictType

const (
	ConflictData       = gosqlite.ConflictData
	ConflictNotFound   = gosqlite.ConflictNotFound
	ConflictConflict   = gosqlite.ConflictConflict
	ConflictConstraint = gosqlite.ConflictConstraint
	ConflictForeignKey = gosqlite.ConflictForeignKey
)

// ConflictAction is a conflict handler's verdict for one conflicting change.
type ConflictAction = gosqlite.ConflictAction

const (
	Omit    = gosqlite.ChangesetOmit    // skip this change
	Replace = gosqlite.ChangesetReplace // overwrite the target row
	Abort   = gosqlite.ChangesetAbort   // abort the whole apply
)

// Capture records every change fn makes to the listed tables and returns the
// serialized changeset. Pass no tables to record every table that has a primary
// key. fn's mutations must run against the session Capture provides (a pinned
// connection); changes made through any other handle are not recorded.
func Capture(ctx context.Context, sess liteorm.Session, tables []string, fn func(ctx context.Context, s liteorm.Session) error) ([]byte, error) {
	bound, gc, release, err := sqlite.Pin(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()

	s, err := gc.CreateSession("main")
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	if len(tables) == 0 {
		if err := s.Attach(""); err != nil { // all primary-keyed tables
			return nil, err
		}
	} else {
		for _, tbl := range tables {
			if err := s.Attach(tbl); err != nil {
				return nil, err
			}
		}
	}

	if err := fn(ctx, bound); err != nil {
		return nil, err
	}
	return s.Changeset()
}

// ApplyOption configures Apply (e.g. WithConflictHandler, WithTableFilter).
type ApplyOption = gosqlite.ApplyOption

// WithConflictHandler resolves each conflicting change. With no handler, any
// conflict aborts the apply.
func WithConflictHandler(h func(ConflictType) ConflictAction) ApplyOption {
	return gosqlite.WithConflictHandler(h)
}

// WithTableFilter restricts which tables a changeset is applied to.
func WithTableFilter(f func(table string) bool) ApplyOption {
	return gosqlite.WithTableFilter(f)
}

// Apply applies a changeset to sess's database on a dedicated connection.
func Apply(ctx context.Context, sess liteorm.Session, cs []byte, opts ...ApplyOption) error {
	_, gc, release, err := sqlite.Pin(ctx, sess)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	return gc.ApplyChangeset(cs, opts...)
}

// Invert reverses a changeset: applying the result undoes the original (an undo
// log). The session connection is only needed to reach the extension.
func Invert(ctx context.Context, sess liteorm.Session, cs []byte) ([]byte, error) {
	_, gc, release, err := sqlite.Pin(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	return gc.InvertChangeset(cs)
}

// Concat concatenates two changesets into one, as if the second were recorded
// immediately after the first.
func Concat(ctx context.Context, sess liteorm.Session, a, b []byte) ([]byte, error) {
	_, gc, release, err := sqlite.Pin(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	return gc.ConcatChangesets(a, b)
}
