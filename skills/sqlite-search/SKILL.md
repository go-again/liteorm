---
name: sqlite-search
description: Use when adding vector (sqlite-vec), full-text (FTS5), or hybrid RRF search to liteorm's SQLite backend.
---

# SQLite search

Import `liteorm.org/dialect/sqlite/search`. These features are SQLite-only and capability-gated: the helpers take a session opened by `liteorm.org/dialect/sqlite` and return `search.ErrUnsupportedBackend` for any other dialect. Every index is a sidecar table keyed by the model's primary key.

## Declarative (recommended)

Declare the indexes on the model; `orm.AutoMigrate` creates the sidecars and the triggers/hooks that keep them in sync, so plain `Repo.Create`/`Update`/`Delete` need no index calls. Search with the typed helpers, which return models in ranked order.

```go
type Article struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32 `orm:"-"` // sidecar-only (not a base column)
}

func (Article) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body"),
		orm.Vector("Embedding", 384).WithMetric(orm.Cosine),
	}
}

orm.AutoMigrate[Article](ctx, db)             // creates articles + articles_fts + articles_vec (+sync)
repo := orm.NewRepo[Article](db)
repo.Create(ctx, &Article{Title: "…", Body: "…", Embedding: vec}) // sidecars sync automatically

s := search.For[Article](db)                                  // typed searcher
near, _  := s.Vector(ctx, queryVec, 5)                        // vector
hits, _  := s.FullText(ctx, search.Term("rocket"), 5)         // full-text
fused, _ := s.Hybrid(ctx, queryVec, search.Term("rocket"), 5) // hybrid (RRF)
for _, h := range near { use(h.Model, h.Score) }
```

- `h.Score`: vector distance for `.Vector` (smaller nearer), BM25 rank for `.FullText`, RRF score for `.Hybrid` (larger better).
- Soft-deleted rows are excluded automatically (loaded through the orm Repo).
- More than one index of a kind → disambiguate with `search.For[Article](db).Field("FieldName")`.

### Declaring: method or tags

- `SearchIndexes() []orm.SearchIndex` — full power: multi-column full-text, `WithWeights`, tokenizer/prefix/detail.
- Tags for the common case: `vec:"dim=384;metric=cosine"` on the embedding, `fts:"tokenize=porter unicode61"` on a text field (`fts5:` is an alias).

### Sync mode (`.WithSync(...)`)

- **Triggers** (default for full-text, and for a vector whose embedding is a stored column): SQL triggers keep the sidecar current on *every* write — bulk and raw `query` writes included.
- **Hooks** (default for a sidecar-only `orm:"-"` vector embedding — no column duplication): synced from the orm write path only; writes bypassing the orm are not indexed.

## Low-level building blocks

Drive a sidecar by hand when there is no model, or you provision/backfill on your own schedule.

```go
v, _ := search.NewVector(ctx, db, "doc_vecs", 5 /* dim */, search.Cosine) // or OpenVector to attach
_ = v.Add(ctx, id /* int64 */, emb /* []float32 */)        // re-Add replaces
keys, _ := v.Search(ctx, queryEmb, 3)                      // []int64, nearest first
scored, _ := v.SearchScored(ctx, queryEmb, 3)              // []Scored{Key, Score=distance}

f, _ := search.NewFullText(ctx, db, "doc_fts")             // or OpenFullText(name, cols...)
_ = f.Add(ctx, id, title+" "+body)
keys, _ = f.Search(ctx, search.And(search.Term("software"), search.Term("flight")), 5)

fused, _ := search.Hybrid(ctx, v, f, queryEmb, search.Term("software"), 4) // []Scored{Key, Score=RRF}
docs, _ := search.Fetch[Doc](ctx, db, keys)                 // or FetchScored from []Scored; preserves order
```

Query builders (re-exported, no gosqlite import): `Term`, `Phrase`, `Prefix`, `And`, `Or`, `Not`, `Near`, `Column`, `Raw`. Metrics: `orm.L2` (default), `orm.Cosine`, `orm.L1`, `orm.Hamming`. RRF tuning: `search.WithK(60)`, `search.WithWeights(wVec, wText)`.

### Custom projection: `*SQL` + `query.Raw[T]`

When your row shape diverges from the model — extra computed columns, a JOIN, a non-ranking filter, a `Score`/`Distance` projection — drop below `search.For[T]` and compose the SQL through gosqlite's typed `fts.SearchSQL` / `vec.KNNSQL` (with their `WithSelect` / `WithJoin` / `WithFilter` options), then execute it with `query.Raw[T](ctx, sess, sql, args...)`. That is the **canonical, intended executor for `*SQL`-returning APIs — the typed pipeline's final step, not a raw-SQL escape hatch.** Reach for `search.For[T]` when you want model-shaped ranked results; drop one level down when the shape diverges.

```go
sql, args, _ := vecTbl.KNNSQL(queryEmb, 10, vec.WithSelect("rowid, distance"), vec.WithFilter("source = ?", src))
hits, _ := query.Raw[searchHit](ctx, db, sql, args...)
```

## MATCH in a composed query

`query.Match("col", q)` is the SQLite `MATCH` operator as a composable predicate — for filtering an FTS5 / spellfix1 / sqlite-vec virtual table inside an ordinary `query.Select` or `orm.Repo` chain, alongside `OrderBy`/`Limit`/other `Filter` predicates. It is feature-gated (rejected at build time off SQLite). Narrower than `search.For[T]` (which returns *ranked* models): use `Match` when you want the operator inside your own query, `search.For[T]` when you want ranking.

```go
hit, _ := orm.NewRepo[Vocab](db).
    Filter(query.Match("word", term), query.Col[int]("scope").Unvalidated().Le(2)).
    OrderBy("distance ASC").
    First(ctx)
```

`scope` is a vtab HIDDEN column (it constrains the search but isn't on the model), so the predicate column is marked `.Unvalidated()` — typed and quoted, but exempt from the model-schema validation that would otherwise reject it. The same applies to FTS5 / sqlite-vec hidden constraint columns.

## Fuzzy correction (spellfix1)

A spellfix1 vocabulary is a *global* virtual table (not a per-model sidecar), so it isn't declared on a model — `search.NewSpellfix` gives a typed handle (create + populate + correct), no raw SQL:

```go
sf, _ := search.NewSpellfix(ctx, db, "vocab")             // create (idempotent)
_ = sf.Add(ctx, "kennedy", "jefferson", "lincoln")        // one tx; dups ignored
hits, _ := sf.Correct(ctx, "kenedy", search.WithLimit(3)) // []Correction{Word,Distance,…}, nearest first
```

Also `Size`, `Drop`, `OpenSpellfix`, `WithMaxDistance(n)`. Importing the `search` package registers the module. To fold a vocabulary match into a *larger* query, `query.Match("word", term)` works against the vtab directly (bind a model, `OrderBy("distance ASC")`).

## Custom SQL functions / REGEXP

gosqlite registers scalar functions globally, so they work through liteorm with no glue: blank-import `gosqlite.org/ext/regexp/auto`, then either write the operator inline — `query.Select[T](db).Where("col REGEXP ?", pattern)` — or use the `sqlite.WhereRegex(column, pattern)` helper, which returns the fragment and bind args and, when the pattern is left-anchored (`^…`), prepends a `GLOB` prefix so SQLite can range-scan an index on the column and run the RE2 match only on the survivors (an unanchored pattern falls back to a plain `REGEXP` scan):

```go
frag, args := sqlite.WhereRegex("title", `^Intro to .* with Go$`)
rows, _ := query.Select[Doc](db).Where(frag, args...).All(ctx)
```

## Implicit columns (rowid)

`sqlite.RowidCol()` is the implicit `rowid` as a typed `query.Column[int64]` (an `.Unvalidated()` column), and `sqlite.Rowid()` is the same as a projection `query.Field` — so a query can filter/order/pluck/project the row key without a raw `query.Expr("rowid")`:

```go
rows, _ := query.Into[Item, reindexRow](ctx,
    query.Select[Item](db).Order(query.Asc(sqlite.RowidCol())),
    sqlite.Rowid(), query.Name("title"))           // typed projection, no raw fragment
ids, _ := query.Pluck[Item, int64](ctx, query.Select[Item](db), sqlite.RowidCol())
```

On an INTEGER PRIMARY KEY table `rowid` is an alias of the PK column, so a Rowid projection reports the PK's name; it's most useful on string-keyed models where `rowid` is a distinct implicit column.

## Pitfalls

- Declarative trigger-mode keeps the index current on all writes; **hook-mode** indexes only sync through the orm Repo — a raw `query` insert won't be indexed.
- `Score` is larger-is-better for `.Hybrid` (RRF) but smaller-is-nearer for `.Vector`/`SearchScored` (distance). Don't compare across them.
- Full-text requires an `int64` primary key (FTS5 is keyed by the integer rowid; a string-PK model errors at migrate). Vector search supports both `int64` and string PKs.
- A helper on a non-`dialect/sqlite` session returns `ErrUnsupportedBackend`.
- **Virtual tables don't honor `ON CONFLICT`.** A vtab's `xUpdate` callback sees an INSERT but not the statement's `ON CONFLICT … DO NOTHING` clause (spellfix1, FTS5, sqlite-vec all ignore it), so an `Upsert(..., OnConflict(...).DoNothing())` against a vtab won't dedup. For idempotent inserts use `Create` and swallow the duplicate — `if err := repo.Create(ctx, &v); err != nil && !errors.Is(err, liteorm.ErrUniqueViolation) { return err }` — or, for a spellfix1 vocabulary, `search.NewSpellfix(...).Add(ctx, words...)`, which inserts with `INSERT OR IGNORE` (a set; duplicates dropped) internally.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/sqlite/search and https://pkg.go.dev/liteorm.org/dialect/sqlite
