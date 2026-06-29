---
name: field-codecs
description: Use when a struct field must be transformed on the way to and from the database — JSON/gob columns, encrypting or compressing a field at rest — without changing its Go type. The codec:<name> tag + liteorm.org/codec.
---

# Field codecs

A field codec transparently encodes a field on write and decodes it on read, **without widening the field's Go type** and without wrapping every read/write. It lives in `liteorm.org/codec` and is attached by tag: `orm:"col,codec:<name>"` (or the gorm-compatible `gorm:"column:col;serializer:<name>"`). Codecs run in the shared scan layer, so the transform applies uniformly through BOTH the `orm` and `query` front-ends — no read path can return the raw (encoded) value by mistake.

Reach for a codec instead of a `sql.Scanner`/`driver.Valuer` when you do not want to change the field's type for a persistence concern (e.g. the field is a plain `[]byte` you want stored encrypted).

## Built-ins (always registered)

| Name | Column | Use |
| --- | --- | --- |
| `json` | TEXT | a struct/map/slice field as JSON |
| `gob` | BLOB | any gob-encodable value |
| `unixtime` | INTEGER | a `time.Time` as Unix seconds |

```go
type Document struct {
	ID   int64          `orm:"id,pk"`
	Meta map[string]any `orm:"meta,codec:json"`
}
orm.AutoMigrate[Document](ctx, db) // `meta` provisioned as TEXT
orm.NewRepo[Document](db).Create(ctx, &Document{Meta: map[string]any{"k": "v"}})
```

The migrated column type follows the codec's storage (TEXT/BLOB/INTEGER), not the Go type — `AutoMigrate` handles it. An explicit `type:` override wins.

## Custom codec (e.g. field encryption)

`codec.Func` builds a codec from two typed functions; register it once during `init`, before first query/migration. The stored type sets the column kind (`string`→TEXT, `[]byte`→BLOB).

```go
func init() {
	codec.Register("secretbox", codec.Func(
		func(plain []byte) ([]byte, error) { return seal(key, plain) },
		func(stored []byte) ([]byte, error) { return open(key, stored) },
	))
}

type Secret struct {
	ID    int64  `orm:"id,pk"`
	Value []byte `orm:"value,codec:secretbox"` // []byte stays []byte; ciphertext at rest
}
```

Read/write `Secret.Value` as plaintext; the column holds ciphertext; both front-ends return plaintext.

## Pitfalls

- **Register before first use.** Resolved at first schema/scan-plan build. Register at `init`; an unregistered codec name fails `AutoMigrate` loudly, naming the field. Last registration for a name wins (override a built-in).
- **NULL preserved.** A nil-pointer field stores NULL (codec not invoked); a NULL column decodes to the zero value.
- **Inspect bytes at rest** by reading the column into a *different* struct whose field has no codec tag (e.g. `query.Raw[struct{ Value []byte }]`), which sees the stored/encoded value.
- A codec spans both front-ends and all typed read paths (orm reads, `query.Select`, `query.Raw[T]` into the model, RETURNING, eager loads).

## Deeper

- API: https://pkg.go.dev/liteorm.org/codec ; guide: docs/guides/field-codecs.md
- Whole-database at-rest encryption is separate (docs/guides/encryption.md) and composes with a codec.
