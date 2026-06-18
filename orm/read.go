package orm

import (
	"context"
	"fmt"
	"slices"

	"liteorm.org/internal/scan"
	"liteorm.org/query"
)

// Scope is a reusable, named read transformer over the query builder — the unit
// teams package common filters in (ActiveOnly, OwnedBy(user), …) and pass to
// Repo.Scopes, mirroring gorm's Scopes. It receives and returns the builder, so
// scopes compose by chaining.
type Scope[T any] func(*query.SelectBuilder[T]) *query.SelectBuilder[T]

// withScope returns a Repo view with sc appended to its read scopes. The slice is
// copied so sibling Repo views never alias each other's scope chain.
func (r *Repo[T]) withScope(sc Scope[T]) *Repo[T] {
	c := *r
	c.readScopes = append(slices.Clone(r.readScopes), sc)
	return &c
}

// Where adds a raw predicate fragment (with "?" placeholders) to the read scope.
// It returns a Repo view, so reads stay the orm convenience layer while delegating
// to the query builder underneath.
func (r *Repo[T]) Where(frag string, args ...any) *Repo[T] {
	return r.withScope(func(b *query.SelectBuilder[T]) *query.SelectBuilder[T] {
		return b.Where(frag, args...)
	})
}

// Filter adds typed predicates (query.Col[V]…) to the read scope.
func (r *Repo[T]) Filter(preds ...query.Predicate) *Repo[T] {
	return r.withScope(func(b *query.SelectBuilder[T]) *query.SelectBuilder[T] {
		return b.Filter(preds...)
	})
}

// OrderBy adds ORDER BY terms to the read scope.
func (r *Repo[T]) OrderBy(terms ...string) *Repo[T] {
	return r.withScope(func(b *query.SelectBuilder[T]) *query.SelectBuilder[T] {
		return b.OrderBy(terms...)
	})
}

// Limit caps the number of rows a read returns.
func (r *Repo[T]) Limit(n int) *Repo[T] {
	return r.withScope(func(b *query.SelectBuilder[T]) *query.SelectBuilder[T] {
		return b.Limit(n)
	})
}

// Offset skips the first n rows of a read.
func (r *Repo[T]) Offset(n int) *Repo[T] {
	return r.withScope(func(b *query.SelectBuilder[T]) *query.SelectBuilder[T] {
		return b.Offset(n)
	})
}

// Scopes appends reusable scopes to the read, in order. Each scope is a function
// over the query builder, so teams can package and share common filters.
func (r *Repo[T]) Scopes(scopes ...Scope[T]) *Repo[T] {
	c := *r
	c.readScopes = slices.Concat(r.readScopes, scopes)
	return &c
}

// selectBuilder constructs a query.SelectBuilder[T] carrying the soft-delete scope
// and every composed read scope — the single place orm reads turn into a query.
func (r *Repo[T]) selectBuilder() (*query.SelectBuilder[T], error) {
	s, err := SchemaOf[T]()
	if err != nil {
		return nil, err
	}
	q := query.Select[T](r.sess)
	if pred, ok := r.scopePredicate(s); ok {
		q = q.Where(pred)
	}
	for _, sc := range r.readScopes {
		q = sc(q)
	}
	return q, nil
}

// First returns the first row matching the current scopes, or liteorm.ErrNoRows.
func (r *Repo[T]) First(ctx context.Context) (T, error) {
	q, err := r.selectBuilder()
	if err != nil {
		var zero T
		return zero, err
	}
	return q.First(ctx)
}

// Count returns how many rows match the current scopes.
func (r *Repo[T]) Count(ctx context.Context) (int64, error) {
	q, err := r.selectBuilder()
	if err != nil {
		return 0, err
	}
	return q.Count(ctx)
}

// Exists reports whether any row matches the current scopes.
func (r *Repo[T]) Exists(ctx context.Context) (bool, error) {
	q, err := r.selectBuilder()
	if err != nil {
		return false, err
	}
	return q.Exists(ctx)
}

// FindInBatches processes every row matching the current scopes in chunks of
// batchSize, calling fn once per non-empty batch. It walks the table by keyset on
// the primary key (WHERE pk > cursor ORDER BY pk LIMIT batchSize), so memory stays
// bounded regardless of total rows — the batched-callback scan gorm and xorm ship,
// for backfills and exports. It honors the soft-delete scope and any composed
// Where/Filter/Scopes, but imposes its own primary-key ordering, so do not combine
// it with OrderBy; it requires a single-column primary key. Iteration stops when a
// short batch is returned or fn returns an error (which is propagated). A
// batchSize <= 0 defaults to 100.
func (r *Repo[T]) FindInBatches(ctx context.Context, batchSize int, fn func(batch []T) error) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	if len(s.PKs) != 1 {
		return fmt.Errorf("orm: FindInBatches requires a single-column primary key on %T", *new(T))
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	pkCol := s.PK.Column
	pkExpr := r.qi(pkCol)
	var cursor any
	for {
		q, err := r.selectBuilder()
		if err != nil {
			return err
		}
		if cursor != nil {
			q = q.Where(pkExpr+" > ?", cursor)
		}
		batch, err := q.OrderBy(pkExpr + " ASC").Limit(batchSize).All(ctx)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err := fn(batch); err != nil {
			return err
		}
		if len(batch) < batchSize {
			return nil
		}
		last := batch[len(batch)-1]
		cursor = scan.Values(&last, []string{pkCol})[0]
	}
}
