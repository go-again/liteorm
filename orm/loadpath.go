package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	liteorm "liteorm.org"
	"liteorm.org/internal/scan"
	"liteorm.org/internal/sqlgen"
)

// LoadPath eager-loads a dotted relation path off roots, running exactly ONE
// batched query per path segment — N+1-safe at every level, with no lazy loading.
// Each segment is a Go relation field name on the type the previous segment
// produced, so "Author.Company" loads each root's Author, then each of those
// authors' Company. A self-referential relation may repeat ("Children.Children")
// for a bounded-depth tree load; depth is exactly the number of segments, so there
// is no unbounded recursion. It is the typed analogue of gorm's Preload("A.B").
func LoadPath[Root any](ctx context.Context, sess liteorm.Session, roots []Root, path string) error {
	if len(roots) == 0 || path == "" {
		return nil
	}
	level := make([]reflect.Value, len(roots))
	for i := range roots {
		level[i] = reflect.ValueOf(&roots[i]).Elem()
	}
	for seg := range strings.SplitSeq(path, ".") {
		if len(level) == 0 {
			return nil // the previous level loaded nothing; deeper levels are empty
		}
		parentType := level[0].Type()
		ps, err := SchemaOfType(parentType)
		if err != nil {
			return err
		}
		rel, ok := ps.Relations[seg]
		if !ok {
			return fmt.Errorf("orm: %s has no relation %q (in path %q)", parentType.Name(), seg, path)
		}
		level, err = loadRelation(ctx, sess, level, rel, loadOpts{})
		if err != nil {
			return err
		}
	}
	return nil
}

// Preloader plans several relation paths off one root slice; Load runs each path
// with one batched query per segment. It is the fluent form of repeated LoadPath
// calls (gorm's chained Preload).
type Preloader[Root any] struct {
	sess  liteorm.Session
	paths []string
}

// NewPreloader starts a preload plan for roots of type Root on sess.
func NewPreloader[Root any](sess liteorm.Session) *Preloader[Root] {
	return &Preloader[Root]{sess: sess}
}

// With adds a dotted relation path to the plan and returns the preloader for
// chaining.
func (p *Preloader[Root]) With(path string) *Preloader[Root] {
	p.paths = append(p.paths, path)
	return p
}

// Load executes every planned path against roots, in order.
func (p *Preloader[Root]) Load(ctx context.Context, roots []Root) error {
	for _, path := range p.paths {
		if err := LoadPath(ctx, p.sess, roots, path); err != nil {
			return err
		}
	}
	return nil
}

// loadRelation runs ONE batched query for rel over the addressable parent struct
// values, assigns the loaded children onto each parent's relation field, and
// returns addressable handles to those stored children — the parents for the next
// path segment. It is the single, reflection-based implementation of eager
// loading that both the generic Load and the path-walking LoadPath delegate to.
func loadRelation(ctx context.Context, sess liteorm.Session, parents []reflect.Value, rel *Relation, opts loadOpts) ([]reflect.Value, error) {
	if len(parents) == 0 {
		return nil, nil
	}
	ps, err := SchemaOfType(parents[0].Type())
	if err != nil {
		return nil, err
	}
	cs, err := SchemaOfType(rel.Target)
	if err != nil {
		return nil, err
	}
	if rel.Kind == RelManyToMany {
		if len(opts.where) > 0 || len(opts.order) > 0 {
			return nil, fmt.Errorf("orm: filtered/ordered eager load is not yet supported for many-to-many relation %q", rel.GoName)
		}
		return loadRelationM2M(ctx, sess, parents, rel, ps, cs)
	}

	ownerField := ps.fieldByColumn(rel.OwnerKey)
	targetField := cs.fieldByColumn(rel.TargetKey)
	if ownerField == nil || targetField == nil {
		return nil, fmt.Errorf("orm: relation %q has unresolved join columns", rel.GoName)
	}

	byKey, ownerVals := groupByOwnerKey(parents, ownerField)
	if len(ownerVals) == 0 {
		return nil, nil
	}
	d := sess.Dialect()
	sel := sqlgen.Select{
		Table: cs.Table,
		Where: []sqlgen.Expr{{SQL: inClause(d, rel.TargetKey, len(ownerVals)), Args: ownerVals}},
	}
	if rel.PolymorphicType != "" { // constrain to this owner type — the table is shared
		sel.Where = append(sel.Where, sqlgen.Expr{
			SQL:  string(d.QuoteIdent(nil, rel.PolymorphicType)) + " = ?",
			Args: []any{rel.PolymorphicValue},
		})
	}
	sel.Where = append(sel.Where, opts.where...) // caller filters (one query, still N+1-safe)
	sel.OrderBy = opts.order
	q, args, err := sel.Build(d)
	if err != nil {
		return nil, err
	}
	rows, err := sess.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	children, err := scan.AllReflect(rows, rel.Target)
	if err != nil {
		return nil, err
	}

	perParent := make([][]reflect.Value, len(parents))
	for ci := 0; ci < children.Len(); ci++ {
		cv := children.Index(ci)
		key := normalizeKey(cv.FieldByIndex(targetField.Index).Interface())
		for _, oi := range byKey[key] {
			perParent[oi] = append(perParent[oi], cv)
		}
	}
	return scatter(parents, rel, perParent), nil
}

// normalizeKey unwraps a sql.Null* join key to its underlying value (a NULL key
// becomes nil) so a nullable foreign key (e.g. a polymorphic owner_id) matches an
// owner's plain primary key in the grouping maps.
func normalizeKey(v any) any {
	switch x := v.(type) {
	case sql.NullInt64:
		if x.Valid {
			return x.Int64
		}
	case sql.NullInt32:
		if x.Valid {
			return int64(x.Int32)
		}
	case sql.NullString:
		if x.Valid {
			return x.String
		}
	default:
		return v
	}
	return nil
}

func loadRelationM2M(ctx context.Context, sess liteorm.Session, parents []reflect.Value, rel *Relation, ps, cs *Schema) ([]reflect.Value, error) {
	ownerField := ps.fieldByColumn(rel.OwnerKey)
	if ownerField == nil {
		return nil, fmt.Errorf("orm: m2m relation %q has unresolved owner key %q", rel.GoName, rel.OwnerKey)
	}
	byKey, ownerVals := groupByOwnerKey(parents, ownerField)
	if len(ownerVals) == 0 {
		return nil, nil
	}
	sqlStr, args := m2mQuery(rel, cs, sess.Dialect(), ownerVals)
	rows, err := sess.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	keys, items, err := scan.AllKeyedReflect(rows, "__k", rel.Target)
	if err != nil {
		return nil, err
	}
	perParent := make([][]reflect.Value, len(parents))
	for i := 0; i < items.Len(); i++ {
		cv := items.Index(i)
		for _, oi := range byKey[keys[i]] {
			perParent[oi] = append(perParent[oi], cv)
		}
	}
	return scatter(parents, rel, perParent), nil
}

// groupByOwnerKey indexes parents by their owner-key value and returns the
// distinct keys (preserving first-seen order) for the IN clause.
func groupByOwnerKey(parents []reflect.Value, ownerField *Field) (map[any][]int, []any) {
	byKey := map[any][]int{}
	var ownerVals []any
	seen := map[any]bool{}
	for i, p := range parents {
		key := normalizeKey(p.FieldByIndex(ownerField.Index).Interface())
		if key == nil {
			continue // a NULL join key can't match a child
		}
		byKey[key] = append(byKey[key], i)
		if !seen[key] {
			seen[key] = true
			ownerVals = append(ownerVals, key)
		}
	}
	return byKey, ownerVals
}

// scatter assigns each parent's grouped children onto its relation field and
// returns addressable handles to the stored children, in parent order. Handles are
// collected only after a parent's field is finalized, so slice reallocation during
// append never invalidates an earlier handle.
func scatter(parents []reflect.Value, rel *Relation, perParent [][]reflect.Value) []reflect.Value {
	var handles []reflect.Value
	for oi, kids := range perParent {
		if len(kids) == 0 {
			continue
		}
		fv := parents[oi].FieldByIndex(rel.Index)
		if rel.IsSlice {
			// Replace, not append: loading a relation is idempotent, so a re-load
			// (or an overlapping preload path) never accumulates duplicate rows.
			sl := reflect.MakeSlice(fv.Type(), 0, len(kids))
			for _, k := range kids {
				sl = reflect.Append(sl, k)
			}
			fv.Set(sl)
			for j := 0; j < fv.Len(); j++ {
				handles = append(handles, fv.Index(j))
			}
		} else if fv.Kind() == reflect.Pointer {
			nv := reflect.New(fv.Type().Elem())
			nv.Elem().Set(kids[0])
			fv.Set(nv)
			handles = append(handles, nv.Elem())
		} else {
			fv.Set(kids[0])
			handles = append(handles, fv)
		}
	}
	return handles
}
