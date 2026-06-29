// Package vault opens a SQLite database stored in a gosqlite.org/vfs/vault
// container: a single file that is, independently, compressed and/or encrypted
// at rest. Unlike the removed snapshot-only compressor it replaces, the default
// Open is LIVE — the database is queried in place, nothing is ever written to
// disk in a weaker form than configured, and durability is per transaction
// (a crash leaves the last committed state intact). Use the returned *liteorm.DB
// exactly like one from sqlite.Open: query, orm, and migrations all work above it.
//
//	db, err := vault.Open("app.db")                 // compressed, live, per-transaction durable
//	db, err := vault.OpenConfig(cfg, vault.Options{ // compressed AND encrypted
//	    Level: vault.CompressionBest, Key: key,     // 32-byte Adiantum / 64-byte AES-XTS key
//	})
//	defer db.Close()
//
// Compression and encryption are independent: Level alone compresses, Key (or
// Options.Recipients, for multi-recipient access via gosqlite.org/crypto/keyring)
// alone encrypts, both does both, neither is a plain container. Derive a key from
// a passphrase with gosqlite.org/vfs/crypto's DeriveKey. Key and Recipients are
// create-time and mutually exclusive.
//
// OpenSnapshot is the archival/distribution model: the file is inflated to a
// plaintext working copy for the session and recompressed at Close, so durability
// is per session and the working copy is plaintext on disk (NOT at-rest
// encryption). Prefer the live Open for anything long-lived.
//
// Reclaiming space is a manual maintenance op (the container plateaus but does
// not shrink mid-session, and plain VACUUM would roughly double the file). Call
// the gosqlite.org/vfs/vault functions directly on the file path: online
// Checkpoint / Trim / CompactLogicalOnline, or offline Compact / CompactLogical,
// plus Pack / Unpack, Snapshot, and the keyring ops Rekey / Rewrap / Members.
package vault

import (
	gosqlite "gosqlite.org"
	gvault "gosqlite.org/vfs/vault"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// Options configures a vault database. It re-exports gosqlite.org/vfs/vault's
// Options so the common case (Level + Key) needs only this package's import; set
// Recipients/Masters for the multi-recipient/keyring features.
type Options = gvault.Options

// Compression is the at-rest compression level, re-exported from
// gosqlite.org/vfs/vault. CompressionNone (the zero value) stores pages raw.
type Compression = gvault.Compression

// Compression levels, re-exported from gosqlite.org/vfs/vault.
const (
	CompressionNone    = gvault.CompressionNone
	CompressionFastest = gvault.CompressionFastest
	CompressionFast    = gvault.CompressionFast
	CompressionDefault = gvault.CompressionDefault
	CompressionBetter  = gvault.CompressionBetter
	CompressionBest    = gvault.CompressionBest
)

// Open opens a live vault database at path, compressed at the default level, with
// the recommended pragma preset. For encryption or a non-default level, use
// OpenConfig.
func Open(path string, opts ...liteorm.Option) (*liteorm.DB, error) {
	return OpenConfig(gosqlite.Config{
		Path:    path,
		Pragmas: gosqlite.RecommendedPragmas(),
	}, Options{Level: CompressionDefault}, opts...)
}

// OpenConfig opens a live vault database from a full gosqlite.Config plus
// Options. The container is queried in place and is durable per transaction;
// cfg.Path must be on disk (in-memory is rejected) and cfg.VFS must be empty.
func OpenConfig(cfg gosqlite.Config, vopts Options, opts ...liteorm.Option) (*liteorm.DB, error) {
	g, err := gvault.Open(cfg, vopts)
	if err != nil {
		return nil, err
	}
	return sqlite.Wrap(g, opts...), nil
}

// OpenSnapshot opens a vault database at path in the snapshot model — inflated to
// a plaintext working copy for the session and recompressed at Close. Durability
// is per session and the working copy is plaintext, so this is for archival /
// distribution / open-modify-close tooling, not a long-lived or crash-critical
// database; prefer Open. Compressed at the default level; use OpenSnapshotConfig
// for a non-default level or a TempDir.
func OpenSnapshot(path string, opts ...liteorm.Option) (*liteorm.DB, error) {
	return OpenSnapshotConfig(gosqlite.Config{
		Path:    path,
		Pragmas: gosqlite.RecommendedPragmas(),
	}, Options{Level: CompressionDefault}, opts...)
}

// OpenSnapshotConfig opens a snapshot-model vault database from a full
// gosqlite.Config plus Options. See OpenSnapshot for the per-session-durability,
// plaintext-working-copy semantics; Options.MaxInflatedSize caps inflation when
// opening a file from an untrusted source.
func OpenSnapshotConfig(cfg gosqlite.Config, vopts Options, opts ...liteorm.Option) (*liteorm.DB, error) {
	g, err := gvault.OpenSnapshot(cfg, vopts)
	if err != nil {
		return nil, err
	}
	return sqlite.Wrap(g, opts...), nil
}
