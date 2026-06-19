# Compressed databases at rest

`sqlite.OpenCompressed(path)` keeps a SQLite database stored compressed on disk and hands back a normal `*liteorm.DB`. It inflates the compressed file into a private working copy, opens that copy, and recompresses it back over the path when you close the handle — so a single `defer db.Close()` both drains the pool and rewrites the compressed file, the same shape as `sqlite.Open` or `sqlite.OpenEncrypted`. The `query` builder, the `orm`, and migrations all work above it unchanged. It is backed by the sibling driver's `gosqlite.org/vfs/compress`.

```go
db, err := sqlite.OpenCompressed("app.db.az")
if err != nil {
	return err
}
defer db.Close() // recompresses the working copy over the path
orm.AutoMigrate[Note](ctx, db)
orm.NewRepo[Note](db).Create(ctx, &Note{Body: "…"})
```

## Snapshot model — read this first

This compresses a database **at rest**. While it is open, it runs from a full, uncompressed working copy under the OS temp directory (or a `TempDir` you choose); the compressed file is rewritten **only at `Close`**. Two consequences follow, and they are the whole reason to reach for this instead of a plain database:

- **Durability is per-session, not per-transaction.** The durable artifact is the snapshot written at `Close`. A crash *while the database is open* leaves the on-disk file at its previous `Close` — no corruption, but changes made in the interrupted session are lost. Every other `Open` in this package is durable per committed transaction; this one is not.
- **The working copy is plaintext** on disk for the lifetime of the handle. So this is **not** a substitute for [at-rest encryption](encryption.md), and the two cannot be composed live — the working copy would have to be encrypted underneath, and compressing already-encrypted data saves nothing.

That makes it a good fit for archival, distribution, shipping an embedded database, and open-modify-close tooling over compressible data — and a poor fit for a database that must stay open continuously or survive a crash mid-session. Keep **one open handle per compressed file**: two handles to the same path each inflate their own working copy and the last `Close` wins.

## Levels and control

`sqlite.OpenCompressedConfig` takes a full `gosqlite.Config` plus `compress.Options`, for a non-default level, a working-copy `TempDir`, or custom pragmas and pool sizing:

```go
db, err := sqlite.OpenCompressedConfig(
	gosqlite.Config{Path: "app.db.az", Pragmas: gosqlite.RecommendedPragmas()},
	compress.Options{Level: compress.CompressionBest},
)
```

The level ladder runs `CompressionFastest` → `CompressionFast` → `CompressionDefault` → `CompressionBetter` → `CompressionBest` (the lower levels are LZ4, the higher ones zstd). The algorithm is auto-detected on read, so a file written at one level always reopens regardless of the level configured later. `CompressionNone` is not meaningful — use a plain `sqlite.Open` for an uncompressed database. Opening a raw, uncompressed `.db` with `OpenCompressed` adopts it: the file is rewritten compressed on `Close`.

## Shipping a compressed file without a session

To compress or inflate a `.db` for distribution, backups, or cold storage without opening it, use the connectionless transforms from `gosqlite.org/vfs/compress` directly — there is nothing for liteorm to add over them:

```go
import "gosqlite.org/vfs/compress"

compress.Pack("app.db.az", "app.db", compress.CompressionBest) // compress an existing .db
compress.Unpack("app.db", "app.db.az")                          // inflate it back
```

For an artifact that must be both compressed *and* encrypted, `Pack` it, then encrypt the resulting file with any encryptor.

## See also

- `examples/compressed` — write compressible rows, observe the on-disk file is a fraction of the logical size, reopen and read it back.
- [At-rest encryption](encryption.md) — transparent, per-transaction page encryption (a different trade-off: durable per transaction, ciphertext on disk).
- [Large objects](large-objects.md) — per-object content compression inside a live database (`lob:"compress=…"`), independent of this whole-database mode; a database that holds large objects still compresses as a whole at `Close`.
