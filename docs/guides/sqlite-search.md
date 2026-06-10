# SQLite search

The `liteorm.org/dialect/sqlite/search` package adds vector nearest-neighbour, full-text, and hybrid search to LiteORM's SQLite backend. These features are SQLite-only and capability-gated: every constructor takes a `liteorm.Session` opened by `liteorm.org/dialect/sqlite` and returns `search.ErrUnsupportedBackend` for any other dialect.

The model is a sidecar index. Your table owns the rows; a vector or full-text index owns the embeddings or terms; your model's `int64` primary key ties them together. A search returns ranked keys (optionally with scores); `search.Load` fetches the model rows by key, preserving rank order. The recipe is the same whether you search by vector, by text, or by both.

## Vector search

`search.NewVector` creates (idempotently) or opens a fixed-dimension vector table with a distance metric. Add embeddings keyed by your primary key, then query for the nearest neighbours.

```go
import "liteorm.org/dialect/sqlite/search"

v, err := search.NewVector(ctx, db, "doc_vecs", dim, search.Cosine)
if err != nil {
	return err
}
v.Add(ctx, doc.ID, embedding) // embedding is []float32; re-adding a key replaces it

keys, err := v.Search(ctx, queryEmbedding, 5) // 5 nearest keys, nearest first
docs, err := search.Load[Doc](ctx, db, keys)  // fetch rows in ranked order
```

The metrics are `search.Cosine` (the usual choice for normalized text embeddings), `search.L2` (the default), `search.L1`, and `search.Hamming` (bit vectors). To inspect distances, use `SearchScored`, which reports each neighbour's raw distance (smaller is nearer):

```go
scored, err := v.SearchScored(ctx, queryEmbedding, 5) // []search.Scored{Key, Score}
docs, err := search.LoadScored[Doc](ctx, db, scored)
```

## Full-text search

`search.NewFullText` creates or opens an FTS5 index over a text column, with the default tokenizer and BM25 ranking. Index text under a key, then query with the builder API.

```go
f, err := search.NewFullText(ctx, db, "doc_fts")
f.Add(ctx, doc.ID, doc.Title+" "+doc.Body) // re-adding a key replaces it

keys, err := f.Search(ctx, search.Term("rocket"), 5) // best (BM25) rank first
docs, err := search.Load[Doc](ctx, db, keys)
```

Queries are built compositionally — there is no raw match-string parsing to get wrong:

```go
search.Term("rocket")                                   // a single term
search.Phrase("orbital", "mechanics")                   // an exact phrase
search.Prefix("rock")                                   // prefix match
search.And(search.Term("software"), search.Term("flight"))
search.Or(search.Term("jazz"), search.Term("blues"))
search.Not(search.Term("space"), search.Term("opera"))  // "space" but not "opera"
search.Near(3, "rocket", "engine")                      // terms within 3 of each other
```

## Hybrid search

`search.Hybrid` runs a vector KNN *and* a full-text query, then fuses the two rankings with reciprocal rank fusion. A key that ranks well in either modality surfaces; one that ranks well in both rises highest — without tuning a brittle score-scale blend. The result is ordered by descending fusion score (larger is better).

```go
fused, err := search.Hybrid(ctx, v, f, queryEmbedding, search.Term("software"), 5)
docs, err := search.LoadScored[Doc](ctx, db, fused)
for i, d := range docs {
	fmt.Printf("%.4f  %s\n", fused[i].Score, d.Title)
}
```

`Hybrid` takes optional fusion knobs — `search.WithK` (the RRF damping constant) and `search.WithWeights` (weighting the vector and full-text rankings, in that order).

## See also

- `examples/search` — vector, full-text, and hybrid search end to end.
- [SQLite changeset](sqlite-changeset.md) — the other SQLite-only extension.
- [Backends reference](../reference/backends.md) — the SQLite backend, how to open it, and at-rest encryption.
- Full API: [`liteorm.org/dialect/sqlite/search`](https://pkg.go.dev/liteorm.org/dialect/sqlite/search) and [`liteorm.org/dialect/sqlite`](https://pkg.go.dev/liteorm.org/dialect/sqlite).
