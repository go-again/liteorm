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

Add `compress=<level>` to the `lob` tag to store content compressed at rest — `lob:"chunk=1m;compress=better"`. The levels run `fastest`, `fast`, `default`, `better`, `best` (a bare `compress` means `default`); the underlying codec is abstracted away. Like the chunk size, the mode is frozen per object when it is created, and reads are mode-agnostic — so raw and compressed objects coexist in one store, and turning compression on for a model never breaks the objects already written under it.

Compression trades CPU and memory for storage, and it changes the I/O profile: a compressed object cannot use in-place incremental BLOB I/O, so a read decompresses a whole chunk and a partial write read-modify-writes one (a write that covers a full chunk skips the read). That makes it a good fit for write-once or sequentially-streamed compressible content — files, logs, JSON — and a poor one for already-compressed payloads or hot random partial updates. Prefer a larger chunk size when compressing, and note that compression composes with [at-rest encryption](encryption.md): content is compressed first, then the pages are encrypted.

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

## Lifecycle

A few properties follow from the engine borrowing a pooled connection per operation, and are worth designing around:

- **Content writes are not part of the ORM transaction.** Each write commits on its own connection, so written content survives an ORM-transaction rollback and vice versa. The supported pattern is to *stream the bytes, then commit the metadata* — the natural order for uploads and files. Don't rely on a `Repo` transaction to undo content already written.
- **Free content on hard-delete.** Deleting the row does not delete its content. Call `lob.Drop` when you hard-delete a row — typically from a `BeforeDelete` hook on the model. A soft-deleted row keeps its content, so `Restore` still has it; only a hard delete should drop.
- **Allocation is leak-tolerant.** The id is allocated on the first write and persisted by a follow-up update; a crash in that narrow window leaves an orphaned content object — wasted space, never corruption — and `Open` already best-effort-frees the object if the persist fails.
- **The import is required.** A model with a large-object field that is migrated without importing `liteorm.org/dialect/sqlite/lob` fails `AutoMigrate` loudly, naming the missing import, rather than silently skipping the content store.

## See also

- The runnable `examples/largeobjects` shows store, range-read, checksum-by-streaming, and delete end to end.
- For *small* binary values that fit in memory, a plain `[]byte` field is simpler — reach for a large object only when the content is big or grows.
