package search

import (
	"context"

	"gosqlite.org/ext/spellfix1"
	_ "gosqlite.org/ext/spellfix1/auto" // registers the spellfix1 vtab module on every connection
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// Spellfix is a spellfix1 fuzzy-match vocabulary — a "did you mean…" word list you
// populate with words and query for the nearest spelling corrections by edit
// distance. Unlike Vector and FullText it is not keyed by a model primary key: it
// is a standalone vocabulary, not a per-row sidecar. Importing this package
// registers the spellfix1 module, so a Spellfix works with no extra wiring; every
// step is typed, so callers never hand-write the vocabulary's SQL.
type Spellfix struct {
	vocab *spellfix1.Vocab
}

// Correction is one fuzzy match: a vocabulary word and how far it is from the
// query term (Damerau-Levenshtein distance; smaller is closer).
type Correction = spellfix1.Match

// CorrectOption bounds a Correct search.
type CorrectOption = spellfix1.CorrectOption

// WithMaxDistance caps the edit distance of returned corrections (default 4).
func WithMaxDistance(n int) CorrectOption { return spellfix1.WithMaxDistance(n) }

// WithLimit caps the number of returned corrections (SQL LIMIT).
func WithLimit(n int) CorrectOption { return spellfix1.WithLimit(n) }

// NewSpellfix creates (idempotently — no error if it already exists) or opens a
// spellfix1 vocabulary named `name` on the session.
func NewSpellfix(ctx context.Context, sess liteorm.Session, name string) (*Spellfix, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	v, err := spellfix1.Create(ctx, g.DB, name, spellfix1.WithIfNotExists())
	if err != nil {
		return nil, err
	}
	return &Spellfix{vocab: v}, nil
}

// OpenSpellfix returns a handle to an existing spellfix1 vocabulary without
// creating it (no I/O; a missing table surfaces at first use).
func OpenSpellfix(sess liteorm.Session, name string) (*Spellfix, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return nil, ErrUnsupportedBackend
	}
	v, err := spellfix1.Open(g.DB, name)
	if err != nil {
		return nil, err
	}
	return &Spellfix{vocab: v}, nil
}

// Name returns the vocabulary's table name.
func (s *Spellfix) Name() string { return s.vocab.Name() }

// Add inserts words into the vocabulary in a single transaction; duplicates are
// ignored (the vocabulary is a set).
func (s *Spellfix) Add(ctx context.Context, words ...string) error {
	return s.vocab.AddMany(ctx, words)
}

// Correct returns the vocabulary words closest to term, nearest (smallest edit
// distance) first. Bound the search with WithMaxDistance / WithLimit.
func (s *Spellfix) Correct(ctx context.Context, term string, opts ...CorrectOption) ([]Correction, error) {
	return s.vocab.Correct(ctx, term, opts...)
}

// Size reports the number of distinct words in the vocabulary.
func (s *Spellfix) Size(ctx context.Context) (int64, error) {
	return s.vocab.Size(ctx)
}

// Drop deletes the vocabulary table; the handle is unusable afterward.
func (s *Spellfix) Drop(ctx context.Context) error {
	return s.vocab.Drop(ctx)
}
