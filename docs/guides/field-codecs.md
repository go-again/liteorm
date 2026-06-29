# Field codecs

A field codec transparently transforms a struct field on its way to and from the database — JSON-encoding a map, gob-encoding a value, or encrypting and compressing a `[]byte` — **without changing the field's Go type** and without a wrapper around every read and write. You keep reading and writing `Secret.Value` as a plain `[]byte`; the bytes on disk are ciphertext.

A codec is attached by name with a struct tag and lives in `liteorm.org/codec`. Because codecs run in LiteORM's shared scan layer, the transform applies uniformly through **both** front-ends: a column written through the `orm` repository and read back through the `query` builder (or the reverse) is encoded and decoded the same way. This is the difference from a `sql.Scanner`/`driver.Valuer`, which forces you to widen the field into a custom type — a codec leaves the field a `[]byte`, a `map`, or a `time.Time`.

## Attach a codec

Tag the field with `codec:<name>`. The built-in codecs `json`, `gob`, and `unixtime` are always available; importing nothing extra is required.

```go
import (
	"liteorm.org/orm"
)

type Document struct {
	ID   int64          `orm:"id,pk"`
	Meta map[string]any `orm:"meta,codec:json"` // stored as JSON text
}

orm.AutoMigrate[Document](ctx, db)              // `meta` is provisioned as TEXT
repo := orm.NewRepo[Document](db)
repo.Create(ctx, &Document{Meta: map[string]any{"k": "v"}}) // marshalled on write
got, _ := repo.Get(ctx, 1)                                  // unmarshalled on read
```

The column type follows the codec's storage, not the Go field's type: `json` stores **TEXT**, `gob` stores **BLOB**, `unixtime` stores **INTEGER**. `AutoMigrate` provisions the column accordingly, so you never hand-write the column type. An explicit `type:` override still wins if you give one.

| Built-in | Stores | For |
| --- | --- | --- |
| `json` | TEXT | a struct, map, or slice field as JSON |
| `gob` | BLOB | any gob-encodable Go value |
| `unixtime` | INTEGER | a `time.Time` as Unix seconds |

A codec naming a name that is not registered fails `AutoMigrate` loudly, naming the field — never a silent fallback to the raw value.

## Write your own

A custom codec is two typed functions — `codec.Func` builds the `codec.Codec` for you, with no `any`-typed assertions on your side. Register it once during `init`, before the first query or migration, and reference it by name from the tag. `Func` infers the column type from the stored type: a `string` result stores as TEXT, a `[]byte` result as BLOB.

The motivating case is encryption at the persistence boundary — the field stays `[]byte`, the column holds ciphertext:

```go
import "liteorm.org/codec"

func init() {
	codec.Register("secretbox", codec.Func(
		func(plain []byte) ([]byte, error) { return seal(key, plain) },   // encode → stored
		func(stored []byte) ([]byte, error) { return open(key, stored) }, // stored → decode
	))
}

type Secret struct {
	ID    int64  `orm:"id,pk"`
	Name  string `orm:"name,unique"`
	Value []byte `orm:"value,codec:secretbox"` // BLOB; ciphertext at rest
}
```

Now application code reads and writes `Secret.Value` as plaintext bytes, the encryption boundary lives entirely in the persistence layer, and a read through *either* front-end returns plaintext — there is no path that hands back ciphertext by mistake. The same shape fits compression, a domain encoding, or any reversible transform.

## Coming from gorm

LiteORM reads gorm's `serializer:` tag key as an alias for `codec:`, and the built-in names (`json`, `gob`, `unixtime`) match gorm's serializers — so a `gorm:"serializer:json"` field works unchanged, no tag rewrite required.

```go
type Post struct {
	ID   int64    `gorm:"column:id;primaryKey"`
	Tags []string `gorm:"column:tags;serializer:json"` // works as-is
}
```

## Notes

- **Register before first use.** Codecs are resolved when a model's schema (or scan plan) is first built. Register them at `init` or early in startup, before the first query or `AutoMigrate`; the last registration for a name wins, so you can override a built-in.
- **NULL is preserved.** A nil-pointer field stores `NULL` rather than an encoded zero, and a `NULL` column decodes to the field's zero value — the codec is not invoked for either.
- **Reads are decoded everywhere.** Every typed read path decodes — `orm` repository reads, `query.Select`, `query.Raw[T]` into the model type, `RETURNING` read-backs, and eager-loaded rows. A raw read into a *different* struct whose field has no codec tag sees the stored (encoded) value, which is how you inspect the bytes at rest.

## See also

- [ORM models](orm.md) and [the query builder](query.md) — the two front-ends a codec spans.
- [At-rest encryption](encryption.md) encrypts the *whole* database file; a codec encrypts *one column* (and they compose).
- API reference: [pkg.go.dev/liteorm.org/codec](https://pkg.go.dev/liteorm.org/codec).
