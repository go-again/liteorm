---
name: vault
description: Use when storing a whole SQLite database compressed and/or encrypted at rest in a gosqlite vfs/vault container (live, per-transaction durable) — the liteorm.org/dialect/sqlite/vault package; distinct from per-object lob compression and from the lighter OpenEncrypted page cipher.
---

# Compressed & encrypted databases (vault)

Import `liteorm.org/dialect/sqlite/vault`. It stores a SQLite database in a gosqlite `vfs/vault` container — one file that is, independently, compressed and/or encrypted at rest. The database is queried **live in place** with **per-transaction durability** (a crash leaves the last committed state). The returned `*liteorm.DB` is used exactly like one from `sqlite.Open`.

```go
db, err := vault.Open("app.db")  // compressed at the default level, live
defer db.Close()
```

## Compression × encryption (independent)

```go
import gosqlite "gosqlite.org"

db, err := vault.OpenConfig(
    gosqlite.Config{Path: "app.db", Pragmas: gosqlite.RecommendedPragmas()},
    vault.Options{Level: vault.CompressionBest, Key: key}, // both; Level alone = compress, Key alone = encrypt
)
```

- Levels: `vault.CompressionNone|Fastest|Fast|Default|Better|Best`. `None` (zero value) stores raw. Compress-then-encrypt order.
- `Key` is the raw cipher key (32-byte Adiantum default / 64-byte AES-XTS); derive from a passphrase with `gosqlite.org/vfs/crypto`'s `DeriveKey`. For shared access use `Options.Recipients` (+ `Masters`/`Writers`, via `gosqlite.org/crypto/keyring`); `Key`/`Recipients` are mutually exclusive, create-time.

## Live vs snapshot

- `vault.Open` / `OpenConfig` — **live**, per-transaction durable, in place. The default; use for anything long-lived.
- `vault.OpenSnapshot` / `OpenSnapshotConfig` — **archival** model: inflated to a plaintext working copy for the session, recompressed at Close. Per-session durability, plaintext on disk (NOT at-rest encryption). For distribution / open-modify-close only.

## Pitfalls

- A live container plateaus but doesn't shrink itself; **never run plain `VACUUM`** (≈doubles the file). Reclaim via the `gosqlite.org/vfs/vault` path functions: online `Checkpoint`/`Trim`/`CompactLogicalOnline`, offline `Compact`/`CompactLogical`; plus `Pack`/`Unpack`, `Snapshot`, `Rekey`/`Rewrap`/`Members`.
- For encryption **without** a container (no format change, lightest deps) use `sqlite.OpenEncrypted` instead — vault is for compression and/or multi-recipient/tamper-evident encryption.
- For compressing big *values* inside a database (not the whole file), use an `orm.LOB` field — see the large-objects skill.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/sqlite/vault. Example: `examples/vault`.
