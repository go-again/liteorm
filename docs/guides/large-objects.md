# Large objects

The `liteorm.org/dialect/sqlite/lob` package gives a model a field whose value is *streamed* content rather than a materialized `[]byte`: a file, an upload, a media blob — anything too large to load whole. It is SQLite-only and capability-gated: every helper takes a `liteorm.Session` opened by `liteorm.org/dialect/sqlite` and returns `lob.ErrUnsupportedBackend` for any other dialect. The streaming engine is the sibling driver's `gosqlite.org/blobstore` (a chunked, growable object store with O(chunk) memory and correct incremental BLOB I/O); this package is the typed, model-bound front door over it.

An ORM normally maps a BLOB column to a `[]byte` struct field, loaded and stored whole — exactly what you must *not* do for a multi-gigabyte upload. A large object is different: the model row stores only an 8-byte object **id** (an INTEGER column), and the bytes live in a content sidecar addressed out-of-band. It is the same *sidecar* shape LiteORM already uses for search — your table owns the row, a content store owns the bytes, and the row's id ties them together — so `AutoMigrate` provisions it the same way it provisions a full-text or vector index.

## Declare the field

Give the model a field of type `orm.LOB`. Importing the `lob` package registers the provisioner, so `AutoMigrate` creates the backing object store automatically.

```go
import (
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/lob"
	"liteorm.org/orm"
)

type File struct {
	ID      int64   `orm:"id,pk"`
	Path    string  `orm:"path,unique"`
	Content orm.LOB `lob:"chunk=1m"` // optional per-object chunk size (k/m/g suffix)
}

orm.AutoMigrate[File](ctx, db) // creates `files` and provisions the `files_content` object store
```

`orm.LOB` is an INTEGER column holding the content object's id. It loads with the row — eight bytes — but the **content never does**. A zero `LOB` is unallocated: the object is created on the first write, and its id is written back both into the struct and into the row. The `lob:"chunk=<size>"` tag tunes the per-object chunk size (frozen per object at creation); omit it for the default. A model with a large object needs a single-column primary key, which keys the object back to its row.

## Compression

Add `compress=<level>` to the `lob` tag to store content compressed at rest — `lob:"chunk=1m;compress=better"`. The levels run `fastest`, `fast`, `default`, `better`, `best` (a bare `compress` means `default`); the underlying compression algorithm is abstracted away. Like the chunk size, the mode is frozen per object when it is created, and reads are mode-agnostic — so raw and compressed objects coexist in one store, and turning compression on for a model never breaks the objects already written under it.

Compression trades CPU and memory for storage, and it changes the I/O profile: a compressed object cannot use in-place incremental BLOB I/O, so a read decompresses a whole chunk and a partial write read-modify-writes one (a write that covers a full chunk skips the read). That makes it a good fit for write-once or sequentially-streamed compressible content — files, logs, JSON — and a poor one for already-compressed payloads or hot random partial updates. Prefer a larger chunk size when compressing, and note that compression composes with [at-rest encryption](encryption.md): content is compressed first, then the pages are encrypted.

## Per-database overrides (operator flags)

The tag is a compile-time default. To let an operator choose chunk size and compression *per database* — wired to a CLI flag, say — override them on `AutoMigrate`, by Go field name:

```go
orm.AutoMigrate[File](ctx, db,
	orm.WithLOBChunkSize("Content", chunk),
	orm.WithLOBCompression("Content", comp),
)
```

Each option overrides only its own knob (the tag still supplies the other); an override naming a field that is not an `orm.LOB` field, a non-positive chunk size, or an unknown compression level fails `AutoMigrate` loudly. Because chunk size and compression are frozen per object at creation, an override is a **default for newly created objects, not a persisted property of the database**: pass the flag consistently across launches (older objects keep the level they were written with, and reads are mode-agnostic), and run `AutoMigrate` before the first `lob.Open` so the store is provisioned with the chosen options before anything allocates. The resolved options are fixed at the store's **first open per database for the process lifetime** — a later `AutoMigrate` with different options is a no-op for a store already opened — so settle the flags once at startup. For read-only introspection — to log the resolved settings at startup — read `orm.SchemaOf[File]().LOBFields` (do not mutate it).

## Stream content

```go
f := &File{Path: "/reports/q3.csv"}
repo.Create(ctx, f) // Content is unallocated until the first write

w, _ := lob.Open(ctx, db, f, "Content")  // io.WriterAt + io.Closer
io.Copy(io.NewOffsetWriter(w, 0), src)   // src is any io.Reader — nothing is buffered whole
w.Close()                                // f.Content is now Allocated()

r, _ := lob.Read(ctx, db, f, "Content")  // io.ReaderAt + io.Closer
mid, _ := io.ReadAll(io.NewSectionReader(r, 1<<20, 4096)) // a 4 KiB range at 1 MiB
r.Close()

size, _ := lob.Size(ctx, db, f, "Content")  // logical length (0 if never written)
lob.Truncate(ctx, db, f, "Content", 0)      // grow (sparse) or shrink
lob.Drop(ctx, db, f, "Content")             // free the content; field resets to unallocated
```

`WriteAt` offsets may be written in any order, and gaps are sparse — they read back as zeros — so a chunked upload that arrives out of order needs no reassembly buffer. `Read` of a field that was never written returns `lob.ErrNotAllocated` (treat it as empty content); `Size` of it returns zero.

## More operations

- **Per-object compression.** Pass `lob.WithCompression(orm.CompressionBest)` to `Open` (or `Truncate`) to set the at-rest compression of the object it allocates, overriding the field's tag default for that object only — raw and compressed objects coexist in one store. Change an existing object's level, or convert it raw↔compressed with content preserved, via `lob.SetCompression(ctx, db, &row, "Content", orm.CompressionBest)`.
- **Stat.** `lob.Stat(ctx, db, &row, "Content")` returns a `lob.Info`: logical `Size`, on-disk `StoredBytes`, the compression `Ratio` / `Level` / `Compressed`, the object's `ChunkSize`, and the `UniqueBytes` / `SharedBytes` split that shows how much a clone shares.
- **Clone.** `lob.Clone(ctx, db, &dst, &src, "Content")` makes `dst`'s content a copy-on-write copy of `src`'s — O(metadata), no bytes copied; the two share storage until one is written. A cheap "duplicate this asset."
- **Write from a reader.** `lob.WriteFrom(ctx, db, &row, "Content", r)` streams an `io.Reader` straight into the object in one engine transaction (allocating on first use) — the "save this upload" one-liner. `lob.WriteFromTx` is its in-transaction variant.
- **Versioning.** `lob.NewVersion(ctx, db, &row, "Content", lob.WithLabel("v1"))` snapshots the current content as an immutable version; `lob.ListVersions` enumerates them, `lob.OpenVersion(…, n)` reads one back. Bound history with a retention policy — `lob.SetRetention(ctx, db, &row, "Content", lob.Policy{KeepVersions: 5})`, `lob.Prune` to enforce it now, or set the default at allocation with the `lob.WithVersioning(lob.Policy{…})` option.
- **Deduplication.** Tag a field `lob:"dedup"` (or set it per database with `orm.WithLOBDedup`) to enable content-addressed block dedup for its store: objects (and versions/clones) that share identical, full-chunk, compressed blocks store them once. It is a store-wide setting fixed at the store's first open.
- **Compression utility.** `lob.Compress(b, orm.CompressionBest)` / `lob.Decompress(b, maxSize)` expose the store's byte compression for compressing your own small values to put in an ordinary column — self-describing output (incompressible input falls back to verbatim), independent of the streaming machinery. (This is byte compression, not a [field codec](field-codecs.md) — for transparent per-column encode/decode use the `codec:` tag.) A non-positive `maxSize` means no decompression cap.

## Transactions

By default a content write commits on its own pooled connection, **outside** any ORM transaction — so streamed content survives an ORM-transaction rollback and vice versa, and the natural pattern is *stream the bytes, then commit the metadata*. When you need the row write and the content write to be **atomic**, use `lob.InTx`: it pins one connection and runs both on a single transaction, so they commit or roll back together.

```go
err := lob.InTx(ctx, db, func(tx *lob.Tx) error {
	f := &File{Path: "/a"}
	if err := orm.NewRepo[File](tx.Session()).Create(ctx, f); err != nil {
		return err
	}
	w, err := lob.OpenTx(ctx, tx, f, "Content")
	if err != nil {
		return err
	}
	_, err = w.WriteAt(data, 0)
	return err // a returned error rolls back BOTH the row and the content
})
```

Run `AutoMigrate` before the first `InTx` (so the store is provisioned) and keep the pool above one connection (the default), since the transaction holds one for its duration. `lob.DropTx` deletes a row's content inside the transaction, for an atomic row+content delete.

## Lifecycle

A few properties follow from the engine borrowing a pooled connection per operation, and are worth designing around:

- **Content writes are not part of the ORM transaction by default.** A plain `lob.Open` write commits on its own connection (see [Transactions](#transactions) above); use `lob.InTx` when the row and the content must be atomic.
- **Free content on hard-delete.** Deleting the row does not delete its content. Call `lob.Drop` when you hard-delete a row — typically from a `BeforeDelete` hook on the model. A soft-deleted row keeps its content, so `Restore` still has it; only a hard delete should drop.
- **Allocation is leak-tolerant.** The id is allocated on the first write and persisted by a follow-up update; a crash in that narrow window leaves an orphaned content object — wasted space, never corruption — and `Open` already best-effort-frees the object if the persist fails.
- **The import is required.** A model with a large-object field that is migrated without importing `liteorm.org/dialect/sqlite/lob` fails `AutoMigrate` loudly, naming the missing import, rather than silently skipping the content store.

## See also

- The runnable `examples/largeobjects` shows store, range-read, checksum-by-streaming, and delete end to end.
- For *small* binary values that fit in memory, a plain `[]byte` field is simpler — reach for a large object only when the content is big or grows.
