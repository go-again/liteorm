package search

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gosqlite.org/fts"
	"gosqlite.org/vec"
	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// Hit is one search result: the loaded model and its relevance score.
type Hit[T any] struct {
	Model T
	// Score's meaning depends on the search: vector distance (smaller is nearer)
	// for Vector, BM25 rank for FullText, and the reciprocal-rank-fusion score
	// (larger is better) for Hybrid.
	Score float64
}

// Searcher runs schema-aware searches for model T against its declared indexes,
// returning loaded models in ranked order. Build one with [For].
type Searcher[T any] struct {
	sess  liteorm.Session
	field string // optional: which field's index to use when a model declares >1 of a kind
}

// For begins a search for model T over sess. The sidecar tables, dimension,
// metric, and columns all come from T's declared search indexes:
//
//	hits, err := search.For[Article](db).Vector(ctx, queryVec, 5)
func For[T any](sess liteorm.Session) *Searcher[T] { return &Searcher[T]{sess: sess} }

// Field selects which index to use when the model declares more than one of the
// same kind, naming the model field the index covers. Returns a new Searcher.
func (s *Searcher[T]) Field(name string) *Searcher[T] { c := *s; c.field = name; return &c }

// Vector runs a nearest-neighbour search over T's vector index, nearest first.
// Soft-deleted models are excluded.
func (s *Searcher[T]) Vector(ctx context.Context, query []float32, k int) ([]Hit[T], error) {
	spec, err := targetSpec[T](dialect.SearchVector, "vector", s.field)
	if err != nil {
		return nil, err
	}
	if !spec.RowidKeyed() {
		return knnKeyed[T](ctx, s.sess, spec, query, k) // non-integer (e.g. string) key
	}
	v, err := OpenVector(s.sess, spec.Name, spec.Dim, metricToken(spec.Metric))
	if err != nil {
		return nil, err
	}
	scored, err := v.SearchScored(ctx, query, k)
	if err != nil {
		return nil, err
	}
	return loadHits[T](ctx, s.sess, scoredKeys(scored))
}

// FullText runs a full-text search over T's FTS index, best (BM25) rank first.
// Soft-deleted models are excluded.
func (s *Searcher[T]) FullText(ctx context.Context, q Query, k int) ([]Hit[T], error) {
	spec, err := targetSpec[T](dialect.SearchFullText, "full-text", s.field)
	if err != nil {
		return nil, err
	}
	f, err := OpenFullText(s.sess, spec.Name, spec.Columns...)
	if err != nil {
		return nil, err
	}
	scored, err := f.SearchScored(ctx, q, k)
	if err != nil {
		return nil, err
	}
	return loadHits[T](ctx, s.sess, scoredKeys(scored))
}

// Hybrid runs a vector and a full-text search over T's indexes and fuses the
// rankings with reciprocal rank fusion (larger score is better). Soft-deleted
// models are excluded. The RRF tuning options (WithK, WithWeights) pass via fuse.
func (s *Searcher[T]) Hybrid(ctx context.Context, query []float32, q Query, k int, fuse ...Option) ([]Hit[T], error) {
	vspec, err := targetSpec[T](dialect.SearchVector, "vector", s.field)
	if err != nil {
		return nil, err
	}
	fspec, err := targetSpec[T](dialect.SearchFullText, "full-text", s.field)
	if err != nil {
		return nil, err
	}
	v, err := OpenVector(s.sess, vspec.Name, vspec.Dim, metricToken(vspec.Metric))
	if err != nil {
		return nil, err
	}
	f, err := OpenFullText(s.sess, fspec.Name, fspec.Columns...)
	if err != nil {
		return nil, err
	}
	scored, err := Hybrid(ctx, v, f, query, q, k, fuse...)
	if err != nil {
		return nil, err
	}
	return loadHits[T](ctx, s.sess, scoredKeys(scored))
}

// OpenVector attaches to an existing vec0 sidecar (no CREATE), the shape
// AutoMigrate provisions — the read-path counterpart to NewVector.
func OpenVector(sess liteorm.Session, name string, dim int, metric Metric) (*Vector, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	tbl, err := vec.Open(g.DB, name, dim, vec.Options{Metric: metric})
	if err != nil {
		return nil, err
	}
	return &Vector{tbl: tbl}, nil
}

// OpenFullText attaches to an existing FTS5 sidecar (no CREATE) given its indexed
// columns — the read-path counterpart to NewFullText for the external-content
// tables AutoMigrate provisions.
func OpenFullText(sess liteorm.Session, name string, cols ...string) (*FullText, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	idx, err := fts.Open[int64, string](g.DB, name, cols...)
	if err != nil {
		return nil, err
	}
	return &FullText{idx: idx}, nil
}

// knnKeyed runs a vector search against a non-integer-keyed (e.g. string-PK)
// vec0 sidecar, loading models by their string key.
func knnKeyed[T any](ctx context.Context, sess liteorm.Session, spec dialect.SearchSpec, query []float32, k int) ([]Hit[T], error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	// The sidecar's key column is named after the model's PK (not vec0's default
	// "id"), matching how it was provisioned.
	tbl, err := vec.OpenKeyed[string](g.DB, spec.Name, spec.Dim,
		vec.Options{Metric: metricToken(spec.Metric)}, vec.WithKeyColumn(spec.PKColumn))
	if err != nil {
		return nil, err
	}
	ns, err := tbl.KNNSlice(ctx, query, k)
	if err != nil {
		return nil, err
	}
	items := make([]scoredKey, len(ns))
	for i, n := range ns {
		items[i] = scoredKey{key: n.Key, score: n.Distance}
	}
	return loadHits[T](ctx, sess, items)
}

// SearchScored runs a full-text query and returns each matching key with its BM25
// rank (the scored counterpart of [FullText.Search]).
func (f *FullText) SearchScored(ctx context.Context, q Query, k int) ([]Scored, error) {
	hits, err := f.idx.SearchSlice(ctx, q, fts.WithRanking())
	if err != nil {
		return nil, err
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	out := make([]Scored, len(hits))
	for i, h := range hits {
		out[i] = Scored{Key: h.Key, Score: h.Rank}
	}
	return out, nil
}

// scoredKey is a ranked primary key (int64 or string) with its score.
type scoredKey struct {
	key   any
	score float64
}

func scoredKeys(s []Scored) []scoredKey {
	out := make([]scoredKey, len(s))
	for i, x := range s {
		out[i] = scoredKey{key: x.Key, score: x.Score}
	}
	return out
}

// loadHits loads each scored key's model through the orm Repo (so soft-deleted
// rows are excluded and scopes apply), preserving rank order. Missing rows are
// skipped. Keys are int64 or string, so Repo.Get addresses either PK shape.
func loadHits[T any](ctx context.Context, sess liteorm.Session, items []scoredKey) ([]Hit[T], error) {
	repo := orm.NewRepo[T](sess)
	out := make([]Hit[T], 0, len(items))
	for _, it := range items {
		row, err := repo.Get(ctx, it.key)
		if errors.Is(err, liteorm.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Hit[T]{Model: row, Score: it.score})
	}
	return out, nil
}

// targetSpec resolves the one search spec of the given kind for T, using the
// optional field name to disambiguate when the model declares more than one.
func targetSpec[T any](kind dialect.SearchKind, label, field string) (dialect.SearchSpec, error) {
	s, err := orm.SchemaOf[T]()
	if err != nil {
		return dialect.SearchSpec{}, err
	}
	specs, err := orm.SearchSpecs[T]() // index-aligned with s.SearchIndexes
	if err != nil {
		return dialect.SearchSpec{}, err
	}
	var found *dialect.SearchSpec
	for i, ix := range s.SearchIndexes {
		if ix.Kind != kind {
			continue
		}
		if field != "" && !slices.Contains(ix.Fields, field) {
			continue
		}
		if found != nil {
			return dialect.SearchSpec{}, fmt.Errorf("search: %T declares more than one %s index; use Field(...) to choose", *new(T), label)
		}
		sp := specs[i]
		found = &sp
	}
	if found == nil {
		return dialect.SearchSpec{}, fmt.Errorf("search: %T has no %s index matching the request", *new(T), label)
	}
	return *found, nil
}

func metricToken(s string) Metric {
	switch s {
	case "cosine":
		return Cosine
	case "l1":
		return L1
	case "hamming":
		return Hamming
	default:
		return L2
	}
}
