package orm

import (
	"context"
	"fmt"
	"reflect"

	liteorm "liteorm.org"
	"liteorm.org/internal/sqlgen"
	"liteorm.org/query"
)

// Association is a typed write handle over the relation `field` of one owner *P
// whose target type is C. It mirrors gorm's db.Model(&owner).Association("Rel")
// for every relation whose foreign key is not on the owner — has-many, has-one,
// and many-to-many — exposing Append, Replace, Delete, Clear, and Count. (For a
// to-one has-one, Replace is the natural setter: it detaches the old target and
// points the new one at the owner.)
//
// It never cascade-saves target rows: targets are linked by their existing
// primary keys, so create them first with a Repo. For has-many/has-one, Delete and
// Clear detach by setting the target's foreign key to NULL (the column must be
// nullable); they never delete target rows. These methods write the database;
// call orm.Load to refresh the owner's field afterwards.
type Association[P any, C any] struct {
	sess  liteorm.Session
	rel   *Relation
	owner *P
	ps    *Schema
	cs    *Schema
}

// Assoc opens an association handle for owner's `field`. It errors when the field
// is not a has-many, has-one, or many-to-many relation whose target type is C.
// Belongs-to is a single foreign key on the owner — set that field and Update the
// owner instead of using an association handle.
func Assoc[P any, C any](sess liteorm.Session, field string, owner *P) (*Association[P, C], error) {
	ps, err := SchemaOf[P]()
	if err != nil {
		return nil, err
	}
	rel, ok := ps.Relations[field]
	if !ok {
		return nil, fmt.Errorf("orm: %s has no relation %q", reflect.TypeFor[P]().Name(), field)
	}
	if rel.Target != reflect.TypeFor[C]() {
		return nil, fmt.Errorf("orm: relation %q targets %s, not %s", field, rel.Target, reflect.TypeFor[C]())
	}
	if rel.Kind == RelBelongsTo {
		return nil, fmt.Errorf("orm: relation %q is belongs-to; set the foreign-key field and Update the owner instead of an association handle", field)
	}
	cs, err := SchemaOf[C]()
	if err != nil {
		return nil, err
	}
	return &Association[P, C]{sess: sess, rel: rel, owner: owner, ps: ps, cs: cs}, nil
}

func (a *Association[P, C]) qi(col string) string {
	return string(a.sess.Dialect().QuoteIdent(nil, col))
}

// ownerKeyVal reads the owner's referenced-key value (the PK the relation joins on).
func (a *Association[P, C]) ownerKeyVal() any {
	f := a.ps.fieldByColumn(a.rel.OwnerKey)
	return reflect.ValueOf(a.owner).Elem().FieldByIndex(f.Index).Interface()
}

// targetPKValues collects each target's primary-key value, requiring a PK.
func (a *Association[P, C]) targetPKValues(targets []*C) ([]any, error) {
	if a.cs.PK == nil {
		return nil, fmt.Errorf("orm: association target %s has no primary key", a.rel.Target.Name())
	}
	vals := make([]any, len(targets))
	for i, t := range targets {
		vals[i] = reflect.ValueOf(t).Elem().FieldByIndex(a.cs.PK.Index).Interface()
	}
	return vals, nil
}

// setTargetFK writes val into each target's foreign-key field in memory (nil → zero).
func (a *Association[P, C]) setTargetFK(targets []*C, val any) {
	a.setTargetCol(a.rel.TargetKey, targets, val)
}

// setTargetType writes the polymorphic type value into each target's type field in
// memory; a no-op for a non-polymorphic relation.
func (a *Association[P, C]) setTargetType(targets []*C, val any) {
	if a.rel.PolymorphicType == "" {
		return
	}
	a.setTargetCol(a.rel.PolymorphicType, targets, val)
}

// setTargetCol writes val into the named column's field on each target (nil → zero).
func (a *Association[P, C]) setTargetCol(col string, targets []*C, val any) {
	f := a.cs.fieldByColumn(col)
	if f == nil {
		return
	}
	rv := reflect.ValueOf(val)
	for _, t := range targets {
		fv := reflect.ValueOf(t).Elem().FieldByIndex(f.Index)
		if rv.IsValid() && rv.Type().AssignableTo(fv.Type()) {
			fv.Set(rv)
		} else {
			fv.Set(reflect.Zero(fv.Type()))
		}
	}
}

// detachSet is the SET list that breaks the owner link: null the foreign key. For
// a polymorphic relation the type column is also cleared — to the empty string
// rather than NULL, since a non-nullable string type column can't scan a NULL back.
// The broken link is signalled by the null owner id.
func (a *Association[P, C]) detachSet() []sqlgen.SetClause {
	set := []sqlgen.SetClause{{Column: a.rel.TargetKey, Arg: nil}}
	if a.rel.PolymorphicType != "" {
		set = append(set, sqlgen.SetClause{Column: a.rel.PolymorphicType, Arg: ""})
	}
	return set
}

// polymorphicWhere appends the type-column constraint to where/args when the
// relation is polymorphic (so a shared table only matches this owner type).
func (a *Association[P, C]) polymorphicWhere(where string, args []any) (string, []any) {
	if a.rel.PolymorphicType == "" {
		return where, args
	}
	return where + " AND " + a.qi(a.rel.PolymorphicType) + " = ?", append(args, a.rel.PolymorphicValue)
}

// Append links targets to the owner. For many-to-many it inserts junction rows
// (idempotent, like Attach); for has-many it points each target's foreign key at
// the owner. Targets must already exist (have primary keys).
func (a *Association[P, C]) Append(ctx context.Context, targets ...*C) error {
	if len(targets) == 0 {
		return nil
	}
	if a.rel.Kind == RelManyToMany {
		return Attach[P, C](ctx, a.sess, a.rel.GoName, a.owner, targets...)
	}
	pkVals, err := a.targetPKValues(targets)
	if err != nil {
		return err
	}
	ownerKey := a.ownerKeyVal()
	d := a.sess.Dialect()
	set := []sqlgen.SetClause{{Column: a.rel.TargetKey, Arg: ownerKey}}
	if a.rel.PolymorphicType != "" { // stamp the owner type alongside the owner id
		set = append(set, sqlgen.SetClause{Column: a.rel.PolymorphicType, Arg: a.rel.PolymorphicValue})
	}
	up := sqlgen.Update{
		Table: a.cs.Table,
		Set:   set,
		Where: []sqlgen.Expr{{SQL: inClause(d, a.cs.PK.Column, len(pkVals)), Args: pkVals}},
	}
	q, args, err := up.Build(d)
	if err != nil {
		return err
	}
	if _, err := a.sess.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	a.setTargetFK(targets, ownerKey)
	a.setTargetType(targets, a.rel.PolymorphicValue)
	return nil
}

// Delete unlinks the given targets from the owner. For many-to-many it removes
// the junction rows; for has-many it nulls the target's foreign key (only for
// rows that currently belong to this owner). It never deletes target rows.
func (a *Association[P, C]) Delete(ctx context.Context, targets ...*C) error {
	if len(targets) == 0 {
		return nil
	}
	if a.rel.Kind == RelManyToMany {
		return Detach[P, C](ctx, a.sess, a.rel.GoName, a.owner, targets...)
	}
	pkVals, err := a.targetPKValues(targets)
	if err != nil {
		return err
	}
	ownerKey := a.ownerKeyVal()
	d := a.sess.Dialect()
	where := a.qi(a.rel.TargetKey) + " = ? AND " + inClause(d, a.cs.PK.Column, len(pkVals))
	args := append([]any{ownerKey}, pkVals...)
	where, args = a.polymorphicWhere(where, args)
	up := sqlgen.Update{
		Table: a.cs.Table,
		Set:   a.detachSet(),
		Where: []sqlgen.Expr{{SQL: where, Args: args}},
	}
	q, qargs, err := up.Build(d)
	if err != nil {
		return err
	}
	if _, err := a.sess.ExecContext(ctx, q, qargs...); err != nil {
		return err
	}
	a.setTargetFK(targets, nil)
	a.setTargetType(targets, nil)
	return nil
}

// Clear unlinks every target from the owner: all junction rows for many-to-many,
// or nulling the foreign key of every owned row for has-many.
func (a *Association[P, C]) Clear(ctx context.Context) error {
	ownerKey := a.ownerKeyVal()
	d := a.sess.Dialect()
	if a.rel.Kind == RelManyToMany {
		del := sqlgen.Delete{
			Table: a.rel.JoinTable,
			Where: []sqlgen.Expr{{SQL: a.qi(a.rel.OwnerFK) + " = ?", Args: []any{ownerKey}}},
		}
		q, args, err := del.Build(d)
		if err != nil {
			return err
		}
		_, err = a.sess.ExecContext(ctx, q, args...)
		return err
	}
	where, wargs := a.polymorphicWhere(a.qi(a.rel.TargetKey)+" = ?", []any{ownerKey})
	up := sqlgen.Update{
		Table: a.cs.Table,
		Set:   a.detachSet(),
		Where: []sqlgen.Expr{{SQL: where, Args: wargs}},
	}
	q, args, err := up.Build(d)
	if err != nil {
		return err
	}
	_, err = a.sess.ExecContext(ctx, q, args...)
	return err
}

// Replace clears the association and then appends targets — the set becomes
// exactly targets.
func (a *Association[P, C]) Replace(ctx context.Context, targets ...*C) error {
	if err := a.Clear(ctx); err != nil {
		return err
	}
	return a.Append(ctx, targets...)
}

// Count returns how many targets are currently linked to the owner.
func (a *Association[P, C]) Count(ctx context.Context) (int64, error) {
	ownerKey := a.ownerKeyVal()
	d := a.sess.Dialect()
	if a.rel.Kind == RelManyToMany {
		sel := sqlgen.Select{
			Table:      a.rel.JoinTable,
			Projection: []string{"count(*)"},
			Where:      []sqlgen.Expr{{SQL: a.qi(a.rel.OwnerFK) + " = ?", Args: []any{ownerKey}}},
		}
		q, args, err := sel.Build(d)
		if err != nil {
			return 0, err
		}
		rows, err := a.sess.QueryContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		defer func() { _ = rows.Close() }()
		var n int64
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0, err
			}
		}
		return n, rows.Err()
	}
	qb := query.Select[C](a.sess).Where(a.qi(a.rel.TargetKey)+" = ?", ownerKey)
	if a.rel.PolymorphicType != "" {
		qb = qb.Where(a.qi(a.rel.PolymorphicType)+" = ?", a.rel.PolymorphicValue)
	}
	return qb.Count(ctx)
}
