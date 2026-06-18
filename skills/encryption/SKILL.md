---
name: encryption
description: Use when opening a SQLite database with at-rest (transparent page-level) encryption — writing/reading an encrypted file, key handling, reopening, and constraints.
---

# At-rest encryption (SQLite)

liteorm opens an encrypted SQLite database through gosqlite's transparent, page-level cipher. Encryption is an **open-time concern**, orthogonal to queries: once the database is open, `query`, `orm`, migrations, and search all work exactly as on an unencrypted database.

## Open with a key

```go
import "liteorm.org/dialect/sqlite"

db, err := sqlite.OpenEncrypted(path, key) // key is 32 bytes; default Adiantum cipher
// use db as a normal *liteorm.DB: orm.AutoMigrate, orm.NewRepo, query.Select, …
```

- `key` is a 32-byte secret — source it from a KMS/secret store, never a literal. Losing it loses the data (no recovery).
- The on-disk file is ciphertext. Reopen with the SAME key to read; a wrong key fails (open or first query), it does not return garbage.

## Full control (cipher / pragmas / pool) via OpenConfig

```go
import gosqlite "gosqlite.org"

db, err := sqlite.OpenConfig(gosqlite.Config{
    Path:       path,
    Pragmas:    gosqlite.RecommendedPragmas(),
    Encryption: &gosqlite.Encryption{Key: key, Cipher: gosqlite.Adiantum},
})
```

## Constraints

- Needs an on-disk path; `:memory:` is rejected (nothing to encrypt at rest).
- Mutually exclusive with a custom VFS (the cipher is itself a VFS layer).
- Per-database-file, set at open — there is no per-table encryption.
- Rotating a key = re-encrypt: open with the old key, copy into a new database opened with the new key.

## Deeper

- Example: `examples/encryption` (write encrypted, verify ciphertext on disk, reopen, reject the wrong key).
- API: https://pkg.go.dev/liteorm.org/dialect/sqlite (`OpenEncrypted`, `OpenConfig`).
