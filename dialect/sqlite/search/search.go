// Package search adds vector (sqlite-vec), full-text (FTS5), and hybrid
// (reciprocal-rank-fusion) search to liteorm's SQLite backend, over the typed
// surface of the sibling driver gosqlite.org. It is SQLite-specific and
// capability-gated: every constructor takes a liteorm.Session opened by
// liteorm.org/dialect/sqlite and returns ErrUnsupportedBackend for any other
// dialect.
//
// Indexes are keyed by your model's int64 primary key — the canonical recipe is
// "your table owns the rows, the sidecar index owns the embeddings/terms, the
// primary key ties them together." A search returns ranked keys (or scored
// keys); Load fetches the model rows by key, preserving rank order. Hybrid runs
// a vector KNN and a full-text query and fuses the two rankings with
// reciprocal-rank-fusion.
package search

import (
	"context"
	"errors"

	"gosqlite.org/fts"
	"gosqlite.org/fusion"
	"gosqlite.org/vec"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

// ErrUnsupportedBackend is returned by a constructor when the session was not
// opened by liteorm.org/dialect/sqlite (the advanced features are SQLite-only).
var ErrUnsupportedBackend = errors.New("liteorm/search: session is not a SQLite database opened by dialect/sqlite")

// Metric mirrors gosqlite/vec's distance functions. Cosine is the usual choice
// for normalized text embeddings; L2 is the default; L1 is Manhattan; Hamming
// applies to bit vectors.
type Metric = vec.Metric

const (
	L2      = vec.L2
	Cosine  = vec.Cosine
	L1      = vec.Dot
	Hamming = vec.Hamming
)

// Scored is one ranked result: a model primary key and its score. For Vector
// the score is the raw distance (smaller is nearer); for Hybrid it is the
// reciprocal-rank-fusion score (larger is better). Each search documents which.
type Scored struct {
	Key   int64
	Score float64
}

// Vector is a sqlite-vec virtual table of fixed-dimension embeddings, keyed by
// your model's int64 primary key (stored as the vec rowid).
type Vector struct {
	tbl *vec.Table
}

// NewVector creates (idempotently — CREATE VIRTUAL TABLE IF NOT EXISTS) or opens
// a sqlite-vec table of the given dimension and distance metric.
func NewVector(ctx context.Context, sess liteorm.Session, name string, dim int, metric Metric) (*Vector, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	tbl, err := vec.Create(ctx, g.DB, name, dim, vec.Options{Metric: metric})
	if err != nil {
		return nil, err
	}
	return &Vector{tbl: tbl}, nil
}

// Add upserts the embedding for a model key. Re-adding the same key replaces it.
func (v *Vector) Add(ctx context.Context, key int64, embedding []float32) error {
	return v.tbl.Insert(ctx, key, embedding)
}

// Search returns the k nearest model keys to the query embedding, nearest first.
func (v *Vector) Search(ctx context.Context, query []float32, k int) ([]int64, error) {
	scored, err := v.SearchScored(ctx, query, k)
	if err != nil {
		return nil, err
	}
	return keys(scored), nil
}

// SearchScored is Search but also reports each neighbour's raw distance (smaller
// is nearer).
func (v *Vector) SearchScored(ctx context.Context, query []float32, k int) ([]Scored, error) {
	ns, err := v.tbl.KNNSlice(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]Scored, len(ns))
	for i, n := range ns {
		out[i] = Scored{Key: n.Rowid, Score: n.Distance}
	}
	return out, nil
}

// FullText is an FTS5 index over a single text column, keyed by your model's
// int64 primary key.
type FullText struct {
	idx *fts.Index[int64, string]
}

// NewFullText creates (idempotently) or opens an FTS5 index. The default
// unicode61 tokenizer and BM25 ranking are used.
func NewFullText(ctx context.Context, sess liteorm.Session, name string) (*FullText, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	idx, err := fts.New[int64, string](ctx, g.DB, name, fts.Options{})
	if err != nil {
		return nil, err
	}
	return &FullText{idx: idx}, nil
}

// Add indexes text under a model key. Re-adding the same key replaces it.
func (f *FullText) Add(ctx context.Context, key int64, text string) error {
	return f.idx.Insert(ctx, fts.Attr[int64, string]{Key: key, Value: text})
}

// Search returns up to k model keys matching the query, best (BM25) rank first.
func (f *FullText) Search(ctx context.Context, q Query, k int) ([]int64, error) {
	hits, err := f.idx.SearchSlice(ctx, q, fts.WithRanking())
	if err != nil {
		return nil, err
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out, nil
}

// Hybrid runs a vector KNN and a full-text query, then fuses the two rankings
// with reciprocal rank fusion (RRF). The result is ordered by descending fusion
// score (larger is better) and capped at k. RRF lets a key that ranks well in
// either modality surface, while one that ranks well in both surfaces highest —
// without tuning a brittle score-scale blend.
func Hybrid(ctx context.Context, v *Vector, f *FullText, embedding []float32, q Query, k int, opts ...Option) ([]Scored, error) {
	vkeys, err := v.Search(ctx, embedding, k)
	if err != nil {
		return nil, err
	}
	tkeys, err := f.Search(ctx, q, k)
	if err != nil {
		return nil, err
	}
	fused, err := fusion.RRF2(vkeys, tkeys, append([]Option{fusion.WithLimit(k)}, opts...)...)
	if err != nil {
		return nil, err
	}
	out := make([]Scored, len(fused))
	for i, r := range fused {
		out[i] = Scored{Key: r.Key, Score: r.Score}
	}
	return out, nil
}

// Option tunes reciprocal rank fusion (e.g. WithK, WithWeights).
type Option = fusion.Option

// WithK overrides the RRF damping constant (default 60).
func WithK(k float64) Option { return fusion.WithK(k) }

// WithWeights weights the vector and full-text rankings, in that order.
func WithWeights(weights ...float64) Option { return fusion.WithWeights(weights...) }

// Load fetches model rows of type T by primary key, preserving the ranked order
// of the supplied keys. Missing keys are skipped. T must be a model the query
// front-end can address (a TableName and an int64 primary key). It issues one
// Get per key, which is the right shape for the small top-k slices search
// returns.
func Load[T any](ctx context.Context, sess liteorm.Session, keys []int64) ([]T, error) {
	repo := query.NewRepo[T](sess)
	out := make([]T, 0, len(keys))
	for _, key := range keys {
		row, err := repo.Get(ctx, key)
		if errors.Is(err, liteorm.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// LoadScored is Load over the keys of a scored result set (Vector.SearchScored
// or Hybrid), preserving order.
func LoadScored[T any](ctx context.Context, sess liteorm.Session, scored []Scored) ([]T, error) {
	return Load[T](ctx, sess, keys(scored))
}

func keys(s []Scored) []int64 {
	out := make([]int64, len(s))
	for i, x := range s {
		out[i] = x.Key
	}
	return out
}

// Full-text query builders, re-exported so callers need not import gosqlite/fts.
type Query = fts.Query

func Term(s string) Query                          { return fts.Term(s) }
func Phrase(tokens ...string) Query                { return fts.Phrase(tokens...) }
func Prefix(s string) Query                        { return fts.Prefix(s) }
func And(qs ...Query) Query                        { return fts.And(qs...) }
func Or(qs ...Query) Query                         { return fts.Or(qs...) }
func Not(positive Query, negatives ...Query) Query { return fts.Not(positive, negatives...) }
func Near(distance int, terms ...string) Query     { return fts.Near(distance, terms...) }
func Column(name string, q Query) Query            { return fts.Column(name, q) }
func Raw(s string) Query                           { return fts.Raw(s) }
