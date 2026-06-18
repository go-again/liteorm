# At-rest encryption

LiteORM opens an encrypted SQLite database through gosqlite's transparent, page-level cipher: you pass a key when opening, and every page written to disk is ciphertext. Encryption is an open-time concern, orthogonal to how you use the data — once the database is open, the `query` builder, the `orm`, migrations, and search all work exactly as on an unencrypted database.

## Opening

`sqlite.OpenEncrypted(path, key)` opens (or creates) an encrypted database with a 32-byte key, using the default Adiantum cipher. The returned `*liteorm.DB` is used exactly like an unencrypted one:

```go
db, err := sqlite.OpenEncrypted("app.db", key) // key is a 32-byte []byte
if err != nil {
	return err
}
orm.AutoMigrate[Note](ctx, db)
orm.NewRepo[Note](db).Create(ctx, &Note{Text: "…"})
```

The on-disk file is ciphertext; reopening requires the same key, and a wrong key fails rather than returning garbage. For full control over the cipher, pragmas, or pool sizing, open from a `gosqlite.Config`:

```go
db, err := sqlite.OpenConfig(gosqlite.Config{
	Path:       "app.db",
	Pragmas:    gosqlite.RecommendedPragmas(),
	Encryption: &gosqlite.Encryption{Key: key, Cipher: gosqlite.Adiantum},
})
```

## Key handling

The key is a 32-byte secret — source it from a key-management service or secret store, never a literal in source. Losing the key means losing the data; there is no recovery path. Rotating a key means re-encrypting: open the database with the old key and copy it into a new database opened with the new key.

## Constraints

- Encryption needs an on-disk path; `:memory:` is rejected, since there is nothing to encrypt at rest.
- It is mutually exclusive with a custom VFS — the cipher is itself a VFS layer.
- It encrypts the whole database file, set at open time; there is no per-table encryption.

## See also

- `examples/encryption` — write encrypted, verify the on-disk bytes are ciphertext, reopen with the key, and watch the wrong key fail.
- [Backends reference](../reference/backends.md) — opening the SQLite backend and its options.
- [SQLite search](sqlite-search.md) — the SQLite-specific query capabilities (independent of encryption).
