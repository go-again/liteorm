# SQLite search

The `liteorm.org/dialect/sqlite/search` package adds vector nearest-neighbour, full-text, and hybrid search to LiteORM's SQLite backend. These features are SQLite-only and capability-gated: the typed helpers and constructors take a `liteorm.Session` opened by `liteorm.org/dialect/sqlite` and return `search.ErrUnsupportedBackend` for any other dialect.

Every index is a *sidecar*: your table owns the rows, an FTS5 or vec0 table owns the terms or embeddings, and your model's `int64` primary key ties them together. There are two ways to drive that sidecar. The **declarative** layer (recommended) declares the index on the model and lets `AutoMigrate` provision it and keep it in sync; the **low-level** layer drives the sidecar by hand for callers that own its lifecycle.

## Declarative search (recommended)

Declare the indexes on the model and let `orm.AutoMigrate` own the rest: it creates the FTS5/vec0 sidecar tables and the triggers (or ORM hooks) that keep them current, so ordinary `Repo.Create`/`Update`/`Delete` need no index bookkeeping.

```go
type Article struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32 `orm:"-"` // sidecar-only (not a base-table column)
}

func (Article) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body"),
		orm.Vector("Embedding", 384).WithMetric(orm.Cosine),
	}
}
```

`AutoMigrate[Article]` then provisions `articles`, `articles_fts`, and `articles_vec`; from there a plain write keeps every index current:

```go
orm.AutoMigrate[Article](ctx, db)
repo := orm.NewRepo[Article](db)
repo.Create(ctx, &Article{Title: "…", Body: "…", Embedding: vec}) // both sidecars sync automatically
```

Search with the typed searcher `search.For[T](db)`, whose `.Vector` / `.FullText` / `.Hybrid` methods return your models in ranked order:

```go
near, _  := search.For[Article](db).Vector(ctx, queryVec, 5)
hits, _  := search.For[Article](db).FullText(ctx, search.Term("rocket"), 5)
fused, _ := search.For[Article](db).Hybrid(ctx, queryVec, search.Term("rocket"), 5)

for _, h := range near {
	// h.Score is the vector distance for .Vector, the BM25 rank for .FullText, and
	// the reciprocal-rank-fusion score for .Hybrid.
	fmt.Println(h.Model.Title, h.Score)
}
```

Soft-deleted rows drop out of results automatically — the searcher loads through the ORM, which honors the soft-delete scope. When a model declares more than one index of the same kind, pick one with `search.For[Article](db).Field("FieldName")`.

### Declaring indexes: method or tags

Both front-ends lower to the same `orm.SearchIndex`:

- A `SearchIndexes() []orm.SearchIndex` method is the typed, full-power form — multi-column full-text, per-column BM25 weights (`WithWeights`), and the tokenizer/prefix/detail options.
- Struct tags cover the common single-field case: `vec:"dim=384;metric=cosine"` on the embedding field, or `fts:"tokenize=porter unicode61"` on a text field. (`fts5:` is accepted as an alias.)

When both are present, the method wins on a sidecar-name collision.

### How writes stay in sync

Each index syncs one of two ways, set with `.WithSync(...)` or left to the default:

- **Triggers** — SQL `AFTER INSERT/UPDATE/DELETE` triggers maintain the sidecar, so *every* write stays indexed: bulk inserts and raw `query` writes that never touch the ORM included. This is the default for full-text (the indexed text already lives on the base table, so it costs nothing) and for a vector whose embedding is a stored column.
- **Hooks** — the ORM write path maintains the sidecar. This is the default for a vector whose embedding is sidecar-only (`orm:"-"`, so the vector is not duplicated on the base table); writes that bypass the ORM are not indexed.

## Query builders

Full-text queries are built compositionally — there is no raw match-string parsing to get wrong. The same builders feed both the searcher's `.FullText` and the low-level `FullText.Search`:

```go
search.Term("rocket")                                   // a single term
search.Phrase("orbital", "mechanics")                   // an exact phrase
search.Prefix("rock")                                   // prefix match
search.And(search.Term("software"), search.Term("flight"))
search.Or(search.Term("jazz"), search.Term("blues"))
search.Not(search.Term("space"), search.Term("opera"))  // "space" but not "opera"
search.Near(3, "rocket", "engine")                      // terms within 3 of each other
```

The vector metrics are `orm.Cosine` (the usual choice for normalized embeddings), `orm.L2` (the default), `orm.L1`, and `orm.Hamming` (bit vectors).

## Low-level building blocks

When you manage the index lifecycle yourself — no model, or a sidecar you provision and backfill on your own schedule — the constructors give you direct handles. `NewVector` and `NewFullText` create (idempotently) or open a sidecar; `Add` upserts a row keyed by your primary key; `Search` returns ranked keys and `Fetch` fetches the model rows in that order.

```go
v, _ := search.NewVector(ctx, db, "doc_vecs", dim, search.Cosine)
v.Add(ctx, doc.ID, embedding)                  // []float32; re-adding a key replaces it
keys, _ := v.Search(ctx, queryEmbedding, 5)    // 5 nearest keys, nearest first
docs, _ := search.Fetch[Doc](ctx, db, keys)    // rows in ranked order

f, _ := search.NewFullText(ctx, db, "doc_fts")
f.Add(ctx, doc.ID, doc.Title+" "+doc.Body)
keys, _ = f.Search(ctx, search.Term("rocket"), 5)
```

`SearchScored` reports each vector neighbour's raw distance (smaller is nearer), and `search.Hybrid` fuses an explicit `Vector` and `FullText` with reciprocal rank fusion — the same fusion the searcher's `.Hybrid` runs, on handles you hold yourself. `Hybrid` takes optional knobs: `search.WithK` (the RRF damping constant) and `search.WithWeights` (weighting the vector and full-text rankings, in that order).

`OpenVector` and `OpenFullText` attach to an already-provisioned sidecar (the shape `AutoMigrate` creates) without re-creating it — the read-path counterparts to the `New*` constructors.

## The MATCH operator in a composed query

The typed searcher above returns *ranked* models. When you instead want the SQLite `MATCH` operator as a plain predicate — to filter an FTS5, spellfix1, or sqlite-vec virtual table inside an ordinary `query.Select` / `orm.Repo` chain, composed with `OrderBy`/`Limit`/other filters — use `query.Match(col, q)`. It is feature-gated on `dialect.FeatMatch`, so it is rejected at build time on a non-SQLite backend rather than emitting unsupported SQL.

```go
hit, err := orm.NewRepo[Vocab](db).
	Filter(query.Match("word", term), query.Col[int]("scope").Le(2)).
	OrderBy("distance ASC").
	First(ctx)
```

Reach for `query.Match` when you want the operator inside your own query; reach for `search.For[T]` when you want ranked results.

## Fuzzy correction with spellfix1

gosqlite's spellfix1 extension provides "did you mean…" spelling correction over a word list. A spellfix1 vocabulary is a *global* table, not a per-model sidecar like FTS5/vec0, so it isn't declared on a model — instead `search.NewSpellfix` gives you a typed handle that creates it, populates it, and corrects terms, with no hand-written SQL:

```go
import "liteorm.org/dialect/sqlite/search"

sf, _ := search.NewSpellfix(ctx, db, "vocab")       // creates the vtab (idempotent)
_ = sf.Add(ctx, "kennedy", "jefferson", "lincoln")  // one transaction; duplicates ignored

hits, _ := sf.Correct(ctx, "kenedy", search.WithLimit(3)) // nearest first, by edit distance
// hits[0].Word == "kennedy", hits[0].Distance > 0
```

`Correct` returns `[]Correction{Word, Distance, Score, MatchLen}`, bounded by `WithMaxDistance(n)` / `WithLimit(n)`; `Size` reports the distinct word count, `Drop` removes the vocabulary, and `OpenSpellfix` attaches to an existing one. Importing the `search` package registers the spellfix1 module, so the handle works with no extra wiring.

To instead fold a vocabulary match into a *larger* query — joining it against other tables, or adding filters — `query.Match("word", term)` works against the vtab directly (bind a model to the vocab table and `OrderBy("distance ASC")`), the same way you'd use any other predicate.

## Regular-expression filters

Beyond the search sidecars, the SQLite backend matches RE2 regular expressions through gosqlite's globally registered `REGEXP` operator. Blank-import `gosqlite.org/ext/regexp/auto` to register it, then build the predicate with `sqlite.WhereRegex`, which returns a WHERE fragment and its bind args. When the pattern is left-anchored (`^…`) it prepends a `GLOB` prefix so SQLite can range-scan an index on the column and run the regex only on the survivors; an unanchored pattern falls back to a plain `REGEXP` scan.

```go
frag, args := sqlite.WhereRegex("title", `^Intro to .* with Go$`)
rows, _ := query.Select[Doc](db).Where(frag, args...).All(ctx)
```

## See also

- `examples/search` — the declarative path and the low-level building blocks, end to end.
- [SQLite changeset](sqlite-changeset.md) — the other SQLite-only extension.
- [Backends reference](../reference/backends.md) — the SQLite backend, how to open it, and at-rest encryption.
- Full API: [`liteorm.org/dialect/sqlite/search`](https://pkg.go.dev/liteorm.org/dialect/sqlite/search) and [`liteorm.org/dialect/sqlite`](https://pkg.go.dev/liteorm.org/dialect/sqlite).
