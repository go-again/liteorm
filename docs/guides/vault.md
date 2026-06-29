# Compressed & encrypted databases (vault)

The `liteorm.org/dialect/sqlite/vault` package stores a SQLite database in a [gosqlite.org/vfs/vault](https://pkg.go.dev/gosqlite.org/vfs/vault) container — a single file that is, independently, compressed and/or encrypted at rest. The database is queried **live, in place**: nothing is ever written to disk in a weaker form than configured, and durability is **per transaction** (a crash leaves the last committed state intact). Use the returned `*liteorm.DB` exactly like one from `sqlite.Open` — `query`, `orm`, and migrations all work above it.

```go
import "liteorm.org/dialect/sqlite/vault"

db, err := vault.Open("app.db")    // compressed at the default level, live
defer db.Close()
orm.AutoMigrate[Note](ctx, db)
orm.NewRepo[Note](db).Create(ctx, &Note{…})
```

## Compression and encryption are independent

`vault.OpenConfig` takes a full `gosqlite.Config` plus `vault.Options`. Set `Level` to compress, `Key` (or `Recipients`) to encrypt, both for both, neither for a plain container:

```go
db, err := vault.OpenConfig(
	gosqlite.Config{Path: "app.db", Pragmas: gosqlite.RecommendedPragmas()},
	vault.Options{Level: vault.CompressionBest, Key: key}, // compressed AND encrypted
)
```

The compression ladder is `CompressionFastest` → `Fast` → `Default` → `Better` → `Best` (LZ4 at the low end, zstd higher), and `CompressionNone` (the zero value) stores pages raw. Compression composes with encryption in the correct order — pages are compressed first, then encrypted.

`Key` is the raw cipher key: 32 bytes for the default Adiantum cipher, 64 bytes for AES-XTS. Derive one from a passphrase with `gosqlite.org/vfs/crypto`'s `DeriveKey`. For shared access without a single shared key, set `Options.Recipients` (and optionally `Masters`/`Writers`) — a random data key is wrapped per recipient via `gosqlite.org/crypto/keyring`, and any holder of a matching identity can open the database; `Key` and `Recipients` are create-time and mutually exclusive.

## Live vs snapshot

`vault.Open` is the **live** model and the right default for anything long-lived: the container is read and written in place, durable per transaction. `vault.OpenSnapshot` is the **archival** model — the file is inflated to a plaintext working copy for the session and recompressed at `Close`, so durability is per session and the working copy is plaintext on disk (so it is *not* at-rest encryption). Reach for snapshot only for distribution / open-modify-close tooling; prefer live `Open` otherwise.

## Reclaiming space

A live container plateaus in size but does not shrink on its own, and a plain `VACUUM` would roughly double the file — so reclaiming is a deliberate maintenance step. Call the `gosqlite.org/vfs/vault` functions on the file path directly (there is nothing for liteorm to wrap over them): online `Checkpoint` / `Trim` / `CompactLogicalOnline`, offline `Compact` / `CompactLogical`, the `Pack` / `Unpack` offline transforms, `Snapshot` for a re-sealed copy, and `Rekey` / `Rewrap` / `Members` for key and access management.

## See also

- `examples/vault` — write rows into a compressed + encrypted live container, observe it is a fraction of the logical size with no plaintext on disk, reopen with the key, and watch the wrong key fail.
- [At-rest encryption](encryption.md) — `sqlite.OpenEncrypted`, the lighter single-key page-encryption VFS, when you want encryption without the container (no format change, smallest dependency footprint). Vault is the answer when you also want compression, multi-recipient access, or tamper-evidence.
- [Large objects](large-objects.md) — for big *values* inside a database rather than compressing the whole file.
