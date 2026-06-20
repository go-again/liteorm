---
name: large-objects
description: Use when storing large or growing binary content (files, uploads, blobs) in SQLite as streamed io.ReaderAt/io.WriterAt instead of loading whole []byte — the orm.LOB field + liteorm.org/dialect/sqlite/lob.
---

# Large objects (streamed content)

Import `liteorm.org/dialect/sqlite/lob`. A large object is binary content too big to load whole — a file, an upload, a media blob — stored in SQLite and read/written incrementally at O(chunk) memory, never materialized as a `[]byte`. SQLite-only and capability-gated: the helpers take a `liteorm.Session` opened by `liteorm.org/dialect/sqlite` and return `lob.ErrUnsupportedBackend` for any other dialect. The streaming engine is the sibling driver's `gosqlite.org/blobstore`; this package is the typed, model-bound front door over it.

The row stores only an 8-byte object **id** (an INTEGER column); the bytes live in a content sidecar and are addressed out-of-band. Importing the package registers the `AutoMigrate` provisioner, so a LOB field provisions its content store the same way a search index provisions its sidecar.

## Declare the field

```go
type File struct {
	ID      int64   `orm:"id,pk"`
	Path    string  `orm:"path,unique"`
	Content orm.LOB `lob:"chunk=1m;compress=better"` // both optional
}

orm.AutoMigrate[File](ctx, db) // creates `files` + the `files_content` object store
```

- `orm.LOB` is an INTEGER column holding the content object's id. It loads with the row (8 bytes); the **bytes never do**. A zero LOB is unallocated — the object is created on the first write and its id is written back into the struct and the row.
- `lob:"chunk=<size>"` tunes the per-object chunk size (k/m/g suffix; default ~64 KiB, frozen per object at creation). Larger chunks suit big streamed files; smaller chunks waste less tail space for many small objects.
- `lob:"compress=<level>"` stores content compressed (`fastest|fast|default|better|best`; a bare `compress` means `default`). The mode is frozen per object at creation and reads are mode-agnostic, so raw and compressed objects coexist in one store. It fits write-once / streamed compressible content (files, logs, JSON); skip it for already-compressed payloads or hot random partial updates (a partial write of a compressed object read-modify-writes its whole chunk). **Prefer a larger `chunk` when compressing**, and it composes with at-rest encryption (content is compressed before the pages are encrypted).
- Needs a single-column primary key (the object id is keyed back to the row by PK).
- **Per-database override (operator flags):** the tag is the default; override per field on AutoMigrate with `orm.WithLOBChunkSize("Content", n)` / `orm.WithLOBCompression("Content", orm.CompressionBest)` (each overrides only its knob; an unknown field name, non-positive chunk, or bad compression level errors). Since chunk/compression are frozen per object at creation, an override is a default for *new* objects, not persisted — pass the flag consistently every launch, and AutoMigrate before the first `lob.Open`. The options are fixed at the store's first open per database for the process; a later AutoMigrate with different options is a no-op. Introspect via read-only `orm.SchemaOf[T]().LOBFields` (never mutate it).

## Stream content

```go
f := &File{Path: "/video.mp4"}
repo.Create(ctx, f)                         // Content unallocated until first write

w, _ := lob.Open(ctx, db, f, "Content")     // io.WriterAt + io.Closer; allocates on first use
io.Copy(io.NewOffsetWriter(w, 0), src)      // or w.WriteAt(p, off) at arbitrary offsets
w.Close()                                   // f.Content is now Allocated()

r, _ := lob.Read(ctx, db, f, "Content")     // io.ReaderAt + io.Closer
chunk, _ := io.ReadAll(io.NewSectionReader(r, 1<<20, 4096)) // read a 4 KiB range at 1 MiB
r.Close()

n, _  := lob.Size(ctx, db, f, "Content")    // logical length in bytes (0 if never written)
lob.Truncate(ctx, db, f, "Content", 0)      // grow (sparse) or shrink
lob.Drop(ctx, db, f, "Content")             // free the content; resets the field to unallocated
```

- `WriteAt` offsets may be written in any order; gaps are sparse (read back as zeros). Each `WriteAt` is durable on return.
- `Read` of a field never written returns `lob.ErrNotAllocated` (treat as empty). `Size` of it returns `0`.

## Lifecycle & pitfalls

- **Content writes are not in the ORM transaction.** The engine commits each write on its own pooled connection, so a content write survives an ORM-tx rollback and vice versa. The supported pattern is **stream bytes, then commit metadata** — which is the natural flow for uploads/files. Don't rely on a `Repo` transaction to roll back already-written content.
- **Free content on hard-delete.** Deleting the row does not delete its content. Call `lob.Drop` when you hard-delete — typically from a `BeforeDelete` hook on the model. Soft-delete keeps the content (so `Restore` still has it); only hard-delete should `Drop`.
- **Allocation is leak-tolerant.** The id is allocated on first write and persisted by a follow-up update; a crash in that window leaves an orphaned content object (wasted space, never corruption). `Open` best-effort-frees the object if the persist fails.
- **Import is required.** A model with a LOB field that is migrated without importing `liteorm.org/dialect/sqlite/lob` fails `AutoMigrate` loudly (it names the missing import) rather than silently skipping the content store.
- A helper on a non-`dialect/sqlite` session returns `lob.ErrUnsupportedBackend`.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/sqlite/lob and https://pkg.go.dev/liteorm.org/orm#LOB
