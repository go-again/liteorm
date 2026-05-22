package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

// loadOpts carries optional filters/order applied to the batched children query of
// an eager load. It stays one query — these constrain the single IN-based fetch,
// they do not make it per-parent.
type loadOpts struct {
	where []sqlgen.Expr
	order []string
}

// LoadOption customizes the batched children query of Load — a filter and/or an
// order on the related rows. Options keep the load N+1-safe (still one query); they
// are raw SQL fragments with "?" placeholders (renumbered per dialect), the same
// escape-hatch style as Repo.Where/OrderBy.
type LoadOption func(*loadOpts)

// LoadWhere restricts the loaded children with a raw predicate fragment, e.g.
// LoadWhere("published_at IS NOT NULL") or LoadWhere("status = ?", "active").
func LoadWhere(frag string, args ...any) LoadOption {
	return func(o *loadOpts) { o.where = append(o.where, sqlgen.Expr{SQL: frag, Args: args}) }
}

// LoadOrderBy orders the loaded children, e.g. LoadOrderBy("created_at DESC").
func LoadOrderBy(terms ...string) LoadOption {
	return func(o *loadOpts) { o.order = append(o.order, terms...) }
}

// Load eager-loads the has-many / belongs-to relation `field` (whose target type
// is C) for the given parents in ONE batched query
// (SELECT ... WHERE TargetKey IN (ownerKeys...)), then assigns the results into
// each parent's field. N+1-safe by construction: the query count is exactly 1
// per Load call, never O(len(parents)). There is no lazy load — you eager-load
// explicitly or you don't have the data.
//
// Optional LoadWhere/LoadOrderBy options filter and order the loaded children
// (still one query); they are not yet supported for many-to-many (a clear error).
// Nested relations are loaded by chaining Load calls one level at a time. A
// belongs-to into a non-pointer struct field cannot represent "no matching row"
// (the field stays its zero value); use a pointer field if that distinction
// matters. An fk override resolves against the target type for has-many and the
// owner type for belongs-to.
func Load[P any, C any](ctx context.Context, sess liteorm.Session, parents []P, field string, opts ...LoadOption) error {
	if len(parents) == 0 {
		return nil
	}
	ps, err := SchemaOf[P]()
	if err != nil {
		return err
	}
	rel, ok := ps.Relations[field]
	if !ok {
		return fmt.Errorf("orm: %s has no relation %q", reflect.TypeFor[P]().Name(), field)
	}
	if rel.Target != reflect.TypeFor[C]() {
		return fmt.Errorf("orm: relation %q targets %s, not %s", field, rel.Target, reflect.TypeFor[C]())
	}
	var lo loadOpts
	for _, o := range opts {
		o(&lo)
	}
	pv := make([]reflect.Value, len(parents))
	for i := range parents {
		pv[i] = reflect.ValueOf(&parents[i]).Elem()
	}
	_, err = loadRelation(ctx, sess, pv, rel, lo)
	return err
}

// m2mQuery builds "SELECT j.<ownerFK> AS __k, t.* FROM <target> t JOIN <join> j
// ON j.<targetFK> = t.<targetKey> WHERE j.<ownerFK> IN (...)" with dialect-correct
// placeholders.
func m2mQuery(rel *Relation, cs *Schema, d dialect.Dialect, ownerVals []any) (string, []any) {
	var b []byte
	b = append(b, "SELECT "...)
	b = d.QuoteIdent(b, "j")
	b = append(b, '.')
	b = d.QuoteIdent(b, rel.OwnerFK)
	b = append(b, " AS "...)
	b = d.QuoteIdent(b, "__k")
	b = append(b, ", "...)
	b = d.QuoteIdent(b, "t")
	b = append(b, ".*"...)
	b = append(b, " FROM "...)
	b = d.QuoteIdent(b, cs.Table)
	b = append(b, " AS "...)
	b = d.QuoteIdent(b, "t")
	b = append(b, " JOIN "...)
	b = d.QuoteIdent(b, rel.JoinTable)
	b = append(b, " AS "...)
	b = d.QuoteIdent(b, "j")
	b = append(b, " ON "...)
	b = d.QuoteIdent(b, "j")
	b = append(b, '.')
	b = d.QuoteIdent(b, rel.TargetFK)
	b = append(b, " = "...)
	b = d.QuoteIdent(b, "t")
	b = append(b, '.')
	b = d.QuoteIdent(b, rel.TargetKey)
	b = append(b, " WHERE "...)
	b = d.QuoteIdent(b, "j")
	b = append(b, '.')
	b = d.QuoteIdent(b, rel.OwnerFK)
	b = append(b, " IN ("...)
	for i := range ownerVals {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.AppendPlaceholder(b, i+1)
	}
	b = append(b, ')')
	return string(b), ownerVals
}

// m2mLink resolves the join-table context for a many-to-many relation: the
// relation, the owner's key value, the target key field, and the dialect.
func m2mLink[P any, C any](sess liteorm.Session, field string, owner *P) (*Relation, any, *Field, dialect.Dialect, error) {
	ps, err := SchemaOf[P]()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rel, ok := ps.Relations[field]
	if !ok || rel.Kind != RelManyToMany {
		return nil, nil, nil, nil, fmt.Errorf("orm: %s has no many-to-many relation %q", reflect.TypeFor[P]().Name(), field)
	}
	cs, err := SchemaOf[C]()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ownerKey := reflect.ValueOf(owner).Elem().FieldByIndex(ps.fieldByColumn(rel.OwnerKey).Index).Interface()
	return rel, ownerKey, cs.fieldByColumn(rel.TargetKey), sess.Dialect(), nil
}

// Attach inserts many-to-many links from owner to each target in the relation's
// join table. It is idempotent: re-attaching an existing pair is a no-op (the
// duplicate-key insert is caught and ignored).
func Attach[P any, C any](ctx context.Context, sess liteorm.Session, field string, owner *P, targets ...*C) error {
	rel, ownerKey, tkField, d, err := m2mLink[P, C](sess, field, owner)
	if err != nil {
		return err
	}
	for _, tgt := range targets {
		tk := reflect.ValueOf(tgt).Elem().FieldByIndex(tkField.Index).Interface()
		ins := sqlgen.Insert{Table: rel.JoinTable, Columns: []string{rel.OwnerFK, rel.TargetFK}, Rows: [][]any{{ownerKey, tk}}}
		q, args, berr := ins.Build(d)
		if berr != nil {
			return berr
		}
		if _, err := sess.ExecContext(ctx, q, args...); err != nil && !errors.Is(err, liteorm.ErrUniqueViolation) {
			return err
		}
	}
	return nil
}

// Detach removes many-to-many links from owner to each target. Removing a link
// that does not exist is a no-op.
func Detach[P any, C any](ctx context.Context, sess liteorm.Session, field string, owner *P, targets ...*C) error {
	rel, ownerKey, tkField, d, err := m2mLink[P, C](sess, field, owner)
	if err != nil {
		return err
	}
	ofk := string(d.QuoteIdent(nil, rel.OwnerFK))
	tfk := string(d.QuoteIdent(nil, rel.TargetFK))
	for _, tgt := range targets {
		tk := reflect.ValueOf(tgt).Elem().FieldByIndex(tkField.Index).Interface()
		del := sqlgen.Delete{Table: rel.JoinTable, Where: []sqlgen.Expr{
			{SQL: ofk + " = ? AND " + tfk + " = ?", Args: []any{ownerKey, tk}},
		}}
		q, args, berr := del.Build(d)
		if berr != nil {
			return berr
		}
		if _, err := sess.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schema) fieldByColumn(col string) *Field {
	for _, f := range s.Fields {
		if f.Column == col {
			return f
		}
	}
	return nil
}

func inClause(d dialect.Dialect, col string, n int) string {
	var b strings.Builder
	b.Write(d.QuoteIdent(nil, col))
	b.WriteString(" IN (")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('?')
	}
	b.WriteByte(')')
	return b.String()
}
