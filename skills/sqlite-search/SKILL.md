---
name: sqlite-search
description: Use when adding vector (sqlite-vec), full-text (FTS5), or hybrid RRF search to liteorm's SQLite backend, or opening an encrypted-at-rest SQLite database.
---

# SQLite search

Import `liteorm.org/dialect/sqlite/search`. These features are SQLite-only and capability-gated: every constructor takes a session opened by `liteorm.org/dialect/sqlite` and returns `search.ErrUnsupportedBackend` for any other dialect.

The recipe: your table owns the rows, a sidecar index owns the embeddings/terms, and the model's `int64` primary key ties them together. A search returns ranked keys; `Load` fetches the model rows by key, preserving rank order.

## Vector (sqlite-vec)

```go
v, err := search.NewVector(ctx, db, "doc_vecs", 5 /* dim */, search.Cosine)
_ = v.Add(ctx, doc.ID /* int64 key */, embedding /* []float32 */)   // re-Add replaces

keys, _ := v.Search(ctx, queryEmb, 3)                 // []int64, nearest first
scored, _ := v.SearchScored(ctx, queryEmb, 3)         // []Scored{Key, Score}; Score = raw distance, smaller is nearer
```

Metrics: `search.L2` (default), `search.Cosine` (normalized text embeddings), `search.L1`, `search.Hamming`.

## Full-text (FTS5)

```go
f, err := search.NewFullText(ctx, db, "doc_fts")      // unicode61 tokenizer, BM25 ranking
_ = f.Add(ctx, doc.ID, doc.Title+" "+doc.Body)        // re-Add replaces

keys, _ := f.Search(ctx, search.Term("rocket"), 5)    // []int64, best BM25 first
```

Query builders (re-exported, no need to import gosqlite/fts): `search.Term(s)`, `search.Phrase(tokens...)`, `search.Prefix(s)`, `search.And(qs...)`, `search.Or(qs...)`, `search.Not(pos, negs...)`, `search.Near(dist, terms...)`, `search.Column(name, q)`, `search.Raw(s)`.

```go
f.Search(ctx, search.And(search.Term("software"), search.Term("flight")), 5)
```

## Hybrid (reciprocal rank fusion)

Runs a vector KNN and a full-text query, then fuses the two rankings with RRF — a key that ranks well in both surfaces highest, without tuning a score-scale blend.

```go
fused, _ := search.Hybrid(ctx, v, f, queryEmb, search.Term("software"), 4)
// []Scored{Key, Score}; Score = RRF score, LARGER is better; ordered desc, capped at k
```

Tuning options: `search.WithK(60)` (RRF damping), `search.WithWeights(wVec, wText)`.

## Load model rows by key (preserves order)

```go
docs, _ := search.Load[Doc](ctx, db, keys)            // from a []int64
docs, _ := search.LoadScored[Doc](ctx, db, scored)    // from []Scored (Vector/Hybrid)
```

T must be a model the query front-end can address: a `TableName()` and an `int64` primary key. Missing keys are skipped. `Load` issues one `Get` per key — the right shape for small top-k slices.

## Encryption at rest

```go
db, err := sqlite.OpenEncrypted(path, key /* []byte */)
// write normally; the on-disk file is ciphertext. Reopen with the same key to read.
```

## Pitfalls

- Indexes are keyed by the model's `int64` PK — that key is the contract between table and sidecar; keep them in sync on insert/delete.
- A constructor on a non-SQLite (or non-`dialect/sqlite`-opened) session returns `ErrUnsupportedBackend`.
- Hybrid `Score` is larger-is-better (RRF); `SearchScored` distance is smaller-is-nearer. Don't compare across them.
- Re-`Add`ing an existing key replaces it; there is no separate update call.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/sqlite/search and https://pkg.go.dev/liteorm.org/dialect/sqlite
