package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	liteorm "liteorm.org"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
	"liteorm.org/query"
)

// Save inserts v when its primary key is zero and updates it otherwise — the
// upsert-by-identity convenience (gorm's Save). It fires the matching
// Before/After Create or Update hooks of the path it takes.
func (r *Repo[T]) Save(ctx context.Context, v *T) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	// Update when every primary-key part is set; otherwise insert. (A composite key
	// with any part still zero is treated as new.)
	if len(s.PKs) > 0 {
		cols := make([]string, len(s.PKs))
		for i, pk := range s.PKs {
			cols[i] = pk.Column
		}
		anyZero := slices.ContainsFunc(scan.Values(v, cols), isZeroValue)
		if !anyZero {
			return r.Update(ctx, v)
		}
	}
	return r.Create(ctx, v)
}

// FirstOrCreate looks up the first row matching conds; if one exists it is loaded
// into *v and created is false. Otherwise v is inserted as-is (Create, firing
// hooks) and created is true. It honors the Repo's soft-delete scope. Mirrors
// gorm's FirstOrCreate (the conds are the lookup; v supplies the new row).
func (r *Repo[T]) FirstOrCreate(ctx context.Context, v *T, conds ...query.Predicate) (created bool, err error) {
	s, err := SchemaOf[T]()
	if err != nil {
		return false, err
	}
	q := query.Select[T](r.sess).Filter(conds...)
	if pred, ok := r.scopePredicate(s); ok {
		q = q.Where(pred)
	}
	got, err := q.First(ctx)
	if err == nil {
		*v = got
		if err := fireAfterFindPtr(ctx, r.sess, v); err != nil {
			return false, err
		}
		return false, nil
	}
	if !errors.Is(err, liteorm.ErrNoRows) {
		return false, err
	}
	return true, r.Create(ctx, v)
}

// FirstOrInit looks up the first row matching conds; if one exists it is loaded
// into *v and found is true. Otherwise *v is left exactly as the caller supplied
// it (its default/attribute values) and NOTHING is written — found is false. It is
// the non-persisting sibling of FirstOrCreate (gorm's FirstOrInit): use it to
// load-or-prepare a value, then decide whether to Save it yourself.
func (r *Repo[T]) FirstOrInit(ctx context.Context, v *T, conds ...query.Predicate) (found bool, err error) {
	s, err := SchemaOf[T]()
	if err != nil {
		return false, err
	}
	q := query.Select[T](r.sess).Filter(conds...)
	if pred, ok := r.scopePredicate(s); ok {
		q = q.Where(pred)
	}
	got, err := q.First(ctx)
	if err == nil {
		*v = got
		if err := fireAfterFindPtr(ctx, r.sess, v); err != nil {
			return false, err
		}
		return true, nil
	}
	if errors.Is(err, liteorm.ErrNoRows) {
		return false, nil // not found: leave v as-is, persist nothing
	}
	return false, err
}

// Upsert inserts v, or on a conflict with oc's columns updates the existing row —
// INSERT ... ON CONFLICT DO UPDATE (gorm/xorm upsert) in a single statement,
// unlike FirstOrCreate's lookup-then-insert. It fires the Create hook set
// (Before/AfterCreate and Before/AfterSave) and stamps auto timestamps. Because
// it is one statement, the hooks fire the same way whether the row is inserted or
// updated — there is no separate update-branch hook. It then delegates to the
// query front-end (which reads a generated key back where the dialect supports
// it). The update path overwrites the columns oc updates; narrow them with
// OnConflict(cols...).DoUpdate(cols...) to preserve a column like created_at.
func (r *Repo[T]) Upsert(ctx context.Context, v *T, oc query.OnConflictSpec) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	ev := &Event[T]{Sess: r.sess, Model: v, Columns: r.writeColumns(s, false)}
	if err := fireBeforeCreate(ctx, ev); err != nil {
		return err
	}
	setAutoTimes(s, v, true)
	if err := query.NewRepo[T](r.sess).Upsert(ctx, v, oc); err != nil {
		return err
	}
	return fireAfterCreate(ctx, ev)
}

// Restore clears the soft-delete timestamp of v's row, bringing a soft-deleted row
// back into the live set — the symmetric partner to Delete. It targets the row by
// primary key regardless of the delete scope (so it can reach an already-deleted
// row), fires Before/AfterUpdate, and returns liteorm.ErrNoRows if no row matches.
// It errors on a model without a soft-delete column.
func (r *Repo[T]) Restore(ctx context.Context, v *T) error {
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	if s.SoftDelete == nil {
		return fmt.Errorf("orm: Restore requires a soft-delete column on %T", *v)
	}
	if len(s.PKs) == 0 {
		return fmt.Errorf("orm: type %T has no primary key", *v)
	}
	ev := &Event[T]{Sess: r.sess, Model: v, Columns: []string{s.SoftDelete.Column}}
	if err := fireBeforeUpdate(ctx, ev); err != nil {
		return err
	}
	up := sqlgen.Update{
		Table: s.Table,
		Set:   []sqlgen.SetClause{{Column: s.SoftDelete.Column, Arg: nil}},
		Where: r.pkWhere(s, v),
	}
	q, args, err := up.Build(r.sess.Dialect())
	if err != nil {
		return err
	}
	res, err := r.sess.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return liteorm.ErrNoRows
	}
	reflect.ValueOf(v).Elem().FieldByIndex(s.SoftDelete.Index).SetZero() // clear in memory too
	return fireAfterUpdate(ctx, ev)
}

// Updates writes only the named columns (matched by column or Go field name) of
// v to its row, firing Before/AfterUpdate hooks — a partial update (gorm's
// Updates). With no columns named it updates the full writable set, like Update.
func (r *Repo[T]) Updates(ctx context.Context, v *T, cols ...string) error {
	if len(cols) == 0 {
		return r.Update(ctx, v)
	}
	return r.Select(cols...).Update(ctx, v)
}

// CreateInBatches inserts vs in chunks of batchSize, firing Before/AfterCreate
// hooks and stamping auto timestamps for every row, and reading generated primary
// keys back into each element where the dialect supports RETURNING/OUTPUT (a
// non-RETURNING dialect inserts the batch without reading keys back). Each chunk
// is one multi-row INSERT — far fewer round trips than Create per row. Mirrors
// gorm's CreateInBatches. A batchSize <= 0 inserts everything in a single batch.
func (r *Repo[T]) CreateInBatches(ctx context.Context, vs []*T, batchSize int) error {
	if len(vs) == 0 {
		return nil
	}
	s, err := SchemaOf[T]()
	if err != nil {
		return err
	}
	if batchSize <= 0 {
		batchSize = len(vs)
	}
	cols := r.writeColumns(s, false)
	for start := 0; start < len(vs); start += batchSize {
		end := min(start+batchSize, len(vs))
		batch := vs[start:end]

		rows := make([][]any, len(batch))
		ops := make([]*Event[T], len(batch))
		for i, v := range batch {
			ev := &Event[T]{Sess: r.sess, Model: v, Columns: cols}
			if err := fireBeforeCreate(ctx, ev); err != nil {
				return err
			}
			setAutoTimes(s, v, true)
			ops[i] = ev
			if rows[i], err = scan.EncodeValues(v, cols); err != nil {
				return err
			}
		}

		ins := sqlgen.Insert{Table: s.Table, Columns: cols, Rows: rows}
		if err := query.InsertManyCapturingPK(ctx, r.sess, ins, batch); err != nil {
			return err
		}

		for i := range batch {
			if err := fireAfterCreate(ctx, ops[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func isZeroValue(v any) bool {
	rv := reflect.ValueOf(v)
	return !rv.IsValid() || rv.IsZero()
}
