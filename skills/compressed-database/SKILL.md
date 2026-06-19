---
name: compressed-database
description: Use when storing a whole SQLite database compressed on disk (archival, distribution, shipping an embedded .db) — sqlite.OpenCompressed and the snapshot-model trade-offs, distinct from per-object lob compression.
---

# Compressed databases at rest (SQLite)

`sqlite.OpenCompressed(path)` keeps a whole SQLite database stored compressed on disk and returns a normal `*liteorm.DB`. It inflates the compressed file into a working copy, opens it, and recompresses over the path on `Close` — `query`, `orm`, and migrations all work above it. Backed by `gosqlite.org/vfs/compress`.

```go
import "liteorm.org/dialect/sqlite"

db, err := sqlite.OpenCompressed("app.db.az")
defer db.Close() // recompresses the working copy over the path — the durable point
```

## Snapshot model — the trade-off (read before using)

While open, the database runs from a **full, uncompressed, plaintext working copy** in the OS temp dir; the compressed file is rewritten **only at `Close`**.

- **Durability is per-session, not per-transaction.** A crash while open reverts the file to its previous `Close` (no corruption, but in-session changes are lost). Unlike every other `Open`.
- **Working copy is plaintext** — not a substitute for encryption, and not composable live with `OpenEncrypted`.
- **One open handle per file** — concurrent handles each inflate their own copy; last `Close` wins.

Fits archival, distribution, shipping an embedded `.db`, open-modify-close tooling over compressible data. Not for an always-open or crash-critical database.

## Level and control

```go
import (
    gosqlite "gosqlite.org"
    "gosqlite.org/vfs/compress"
)

db, err := sqlite.OpenCompressedConfig(
    gosqlite.Config{Path: "app.db.az", Pragmas: gosqlite.RecommendedPragmas()},
    compress.Options{Level: compress.CompressionBest}, // Fastest|Fast|Default|Better|Best; LZ4 low, zstd high
)
```

- Level auto-detected on read; a file written at one level reopens at any level. `CompressionNone` is meaningless here (use `sqlite.Open`).
- Opening a raw `.db` adopts it — rewritten compressed on `Close`. `Options.TempDir` relocates the working copy.

## Offline transforms (no session)

For shipping/backup without opening, call the file transforms directly — liteorm adds nothing over them:

```go
compress.Pack("app.db.az", "app.db", compress.CompressionBest) // compress an existing .db
compress.Unpack("app.db", "app.db.az")                          // inflate it back
```

For compressed *and* encrypted: `Pack`, then encrypt the artifact with any encryptor.

## Not the same as lob compression

This compresses the **whole database file** at rest. To compress **individual large-object values** inside a live, per-transaction-durable database, use an `orm.LOB` field with `lob:"compress=…"` (see the large-objects skill) — independent of this mode.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/sqlite (`OpenCompressed`, `OpenCompressedConfig`).
- Example: `examples/compressed`.
