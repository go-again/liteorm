package orm

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"liteorm.org/internal/scan"
)

var colSetCache sync.Map // reflect.Type -> map[string]bool

// columnsOnly builds a columns-only schema (no relations) for t. The relation
// inference helpers use it so resolving a target type's columns/PK never
// recurses back into relation building.
func columnsOnly(t reflect.Type) *Schema {
	tmp := &Schema{Type: t, Relations: map[string]*Relation{}}
	walkColumns(t, nil, "", tmp)
	if len(tmp.PKs) == 1 { // mirror buildSchema's single-PK convenience for PK lookups
		tmp.PK = tmp.PKs[0]
	}
	return tmp
}

// columnSet returns the column names of t (no relations), cached. Used to verify
// inferred foreign keys exist on the referenced side.
func columnSet(t reflect.Type) map[string]bool {
	if c, ok := colSetCache.Load(t); ok {
		return c.(map[string]bool)
	}
	tmp := columnsOnly(t)
	set := make(map[string]bool, len(tmp.Fields))
	for _, f := range tmp.Fields {
		set[f.Column] = true
	}
	actual, _ := colSetCache.LoadOrStore(t, set)
	return actual.(map[string]bool)
}

func pkColumn(t reflect.Type) (string, bool) {
	tmp := columnsOnly(t)
	if tmp.PK != nil {
		return tmp.PK.Column, true
	}
	return "", false
}

func pkField(t reflect.Type) *Field {
	tmp := columnsOnly(t)
	return tmp.PK
}

var goNameCache sync.Map // reflect.Type -> map[string]string (Go field name -> column)

func goNameColumns(t reflect.Type) map[string]string {
	if c, ok := goNameCache.Load(t); ok {
		return c.(map[string]string)
	}
	tmp := columnsOnly(t)
	m := make(map[string]string, len(tmp.Fields))
	for _, f := range tmp.Fields {
		m[f.GoName] = f.Column
	}
	actual, _ := goNameCache.LoadOrStore(t, m)
	return actual.(map[string]string)
}

// resolveCol maps a foreignKey/references override to a column for type t. The
// override may be a column name OR a Go field name (gorm uses field names); a Go
// field-name match wins, else it is assumed to already be a column.
func resolveCol(t reflect.Type, nameOrField string) string {
	if nameOrField == "" {
		return ""
	}
	if col, ok := goNameColumns(t)[nameOrField]; ok {
		return col
	}
	return nameOrField
}

// inferRelation resolves an association field into a Relation, inferring the
// foreign key by convention (ownerType+"_id" / targetType+"_id") and HARD-ERRORING
// when the inferred key is absent — never a silent guess. Overridable via
// `orm:"fk:<col>"` / `orm:"references:<col>"` (or gorm foreignKey/references).
func inferRelation(owner reflect.Type, sf reflect.StructField, idx []int, s *Schema) (*Relation, error) {
	ft := sf.Type
	isSlice := ft.Kind() == reflect.Slice
	target := ft
	for target.Kind() == reflect.Pointer || target.Kind() == reflect.Slice {
		target = target.Elem()
	}
	fkOverride, refOverride := relTagOverride(sf)

	if name, idCol, typeCol, value, ok := polymorphicSpec(sf); ok {
		return inferPolymorphic(owner, target, sf, idx, s, isSlice, name, idCol, typeCol, value, refOverride)
	}

	if isSlice {
		if jt, ok := m2mTable(sf); ok {
			return inferM2M(owner, target, sf, idx, s, jt)
		}
		// has-many: the FK lives on the target, referencing the owner's PK.
		if s.PK == nil {
			return nil, fmt.Errorf("orm: %s.%s (has-many) needs a primary key on %s", owner.Name(), sf.Name, owner.Name())
		}
		ownerKey := s.PK.Column
		if refOverride != "" {
			ownerKey = resolveCol(owner, refOverride)
		}
		targetKey := resolveCol(target, fkOverride)
		if targetKey == "" {
			targetKey = scan.Snake(owner.Name()) + "_id"
		}
		if !columnSet(target)[targetKey] {
			return nil, fmt.Errorf("orm: %s.%s (has-many %s): inferred foreign key %q not found on %s — add the column or set `orm:\"fk:<col>\"`",
				owner.Name(), sf.Name, target.Name(), targetKey, target.Name())
		}
		return &Relation{GoName: sf.Name, Kind: RelHasMany, Target: target, OwnerKey: ownerKey, TargetKey: targetKey, Index: idx, IsSlice: true}, nil
	}

	return inferSingular(owner, target, sf, idx, s, fkOverride, refOverride)
}

// inferSingular resolves a non-slice struct/pointer association into either
// belongs-to (the FK lives on the owner, referencing the target's PK) or has-one
// (the FK lives on the target, referencing the owner's PK). It prefers belongs-to
// when the owner carries the foreign key, and otherwise falls through to has-one —
// the owner-first resolution gorm uses. An fk/references override naturally routes
// to the right side: it is read against the owner for belongs-to and the target
// for has-one. A genuine miss is a hard error naming both possibilities, never a
// silent guess.
func inferSingular(owner, target reflect.Type, sf reflect.StructField, idx []int, s *Schema, fkOverride, refOverride string) (*Relation, error) {
	// belongs-to: the FK lives on the owner, referencing the target's PK.
	ownerFK := resolveCol(owner, fkOverride)
	if ownerFK == "" {
		ownerFK = scan.Snake(target.Name()) + "_id"
	}
	if columnSet(owner)[ownerFK] {
		targetKey := resolveCol(target, refOverride)
		if targetKey == "" {
			pk, ok := pkColumn(target)
			if !ok {
				return nil, fmt.Errorf("orm: %s.%s (belongs-to %s) which has no primary key", owner.Name(), sf.Name, target.Name())
			}
			targetKey = pk
		}
		return &Relation{GoName: sf.Name, Kind: RelBelongsTo, Target: target, OwnerKey: ownerFK, TargetKey: targetKey, Index: idx, IsSlice: false, Constraint: relWantsConstraint(sf)}, nil
	}

	// has-one: the FK lives on the target, referencing the owner's PK.
	if s.PK == nil {
		return nil, fmt.Errorf("orm: %s.%s (has-one %s) needs a primary key on %s", owner.Name(), sf.Name, target.Name(), owner.Name())
	}
	ownerKey := s.PK.Column
	if refOverride != "" {
		ownerKey = resolveCol(owner, refOverride)
	}
	targetFK := resolveCol(target, fkOverride)
	if targetFK == "" {
		targetFK = scan.Snake(owner.Name()) + "_id"
	}
	if !columnSet(target)[targetFK] {
		return nil, fmt.Errorf("orm: %s.%s (%s): no foreign key found — for belongs-to add %q on %s, for has-one add %q on %s, or set `orm:\"fk:<col>\"`",
			owner.Name(), sf.Name, target.Name(), ownerFK, owner.Name(), targetFK, target.Name())
	}
	return &Relation{GoName: sf.Name, Kind: RelHasOne, Target: target, OwnerKey: ownerKey, TargetKey: targetFK, Index: idx, IsSlice: false}, nil
}

// inferPolymorphic builds a polymorphic has-many (slice) or has-one (non-slice)
// relation: the target carries <name>_id + <name>_type columns, and loads/writes
// constrain the type column to a constant (the owner's table name by default) so
// one table can be owned by several owner types — gorm's `polymorphic:Owner`. The
// id/type columns are overridable (polymorphicId / polymorphicType) and the
// constant via polymorphicValue. A missing column on the target is a hard error
// naming both. The inverse direction (target.Owner resolving back to one of
// several owner types) is out of scope — see the associations guide.
func inferPolymorphic(owner, target reflect.Type, sf reflect.StructField, idx []int, s *Schema, isSlice bool, name, idCol, typeCol, value, refOverride string) (*Relation, error) {
	if s.PK == nil {
		return nil, fmt.Errorf("orm: %s.%s (polymorphic) needs a single primary key on %s", owner.Name(), sf.Name, owner.Name())
	}
	if idCol == "" {
		idCol = scan.Snake(name) + "_id"
	} else {
		idCol = resolveCol(target, idCol)
	}
	if typeCol == "" {
		typeCol = scan.Snake(name) + "_type"
	} else {
		typeCol = resolveCol(target, typeCol)
	}
	cols := columnSet(target)
	if !cols[idCol] || !cols[typeCol] {
		return nil, fmt.Errorf("orm: %s.%s (polymorphic %s): %s must have both %q and %q columns — add them or set polymorphicId/polymorphicType",
			owner.Name(), sf.Name, target.Name(), target.Name(), idCol, typeCol)
	}
	ownerKey := s.PK.Column
	if refOverride != "" {
		ownerKey = resolveCol(owner, refOverride)
	}
	if value == "" {
		value = s.Table
	}
	kind := RelHasOne
	if isSlice {
		kind = RelHasMany
	}
	return &Relation{
		GoName: sf.Name, Kind: kind, Target: target,
		OwnerKey: ownerKey, TargetKey: idCol,
		PolymorphicType: typeCol, PolymorphicValue: value,
		Index: idx, IsSlice: isSlice,
	}, nil
}

func inferM2M(owner, target reflect.Type, sf reflect.StructField, idx []int, s *Schema, jt string) (*Relation, error) {
	if s.PK == nil {
		return nil, fmt.Errorf("orm: %s.%s (many-to-many) needs a primary key on %s", owner.Name(), sf.Name, owner.Name())
	}
	tpk, ok := pkColumn(target)
	if !ok {
		return nil, fmt.Errorf("orm: %s.%s (many-to-many %s) which has no primary key", owner.Name(), sf.Name, target.Name())
	}
	ownerFK, targetFK := joinKeys(sf)
	if ownerFK == "" {
		ownerFK = scan.Snake(owner.Name()) + "_id"
	}
	if targetFK == "" {
		targetFK = scan.Snake(target.Name()) + "_id"
	}
	return &Relation{
		GoName: sf.Name, Kind: RelManyToMany, Target: target, Index: idx, IsSlice: true,
		OwnerKey: s.PK.Column, TargetKey: tpk,
		JoinTable: jt, OwnerFK: ownerFK, TargetFK: targetFK,
	}, nil
}

// ormRelOpts splits a relation field's `orm:"..."` tag into key:value options.
// Unlike a column field, EVERY comma token is a directive (there is no leading
// column name), so all tokens are processed.
func ormRelOpts(sf reflect.StructField) (map[string]string, bool) {
	tag, ok := sf.Tag.Lookup("orm")
	if !ok {
		return nil, false
	}
	out := map[string]string{}
	for o := range strings.SplitSeq(tag, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(o), ":")
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out, true
}

func m2mTable(sf reflect.StructField) (string, bool) {
	if o, ok := ormRelOpts(sf); ok {
		if v, ok := o["m2m"]; ok {
			return v, true
		}
		if v, ok := o["many2many"]; ok {
			return v, true
		}
		return "", false
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		if v, ok := scan.ParseGormTag(tag)["many2many"]; ok {
			return v, true
		}
	}
	return "", false
}

func joinKeys(sf reflect.StructField) (ownerFK, targetFK string) {
	if o, ok := ormRelOpts(sf); ok {
		ownerFK = firstNonEmpty(o["joinfk"], o["joinforeignkey"])
		targetFK = firstNonEmpty(o["joinref"], o["joinreferences"])
		return ownerFK, targetFK
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		g := scan.ParseGormTag(tag)
		ownerFK = g["joinforeignkey"]
		targetFK = g["joinreferences"]
	}
	return ownerFK, targetFK
}

// polymorphicSpec reads a polymorphic relation tag from a field's orm or gorm
// tags. `polymorphic:Owner` declares that the target carries owner_id + owner_type
// columns (derived from the "Owner" prefix); polymorphicId / polymorphicType
// override those column names and polymorphicValue overrides the constant written
// to the type column. It returns ok=false when no polymorphic tag is present.
func polymorphicSpec(sf reflect.StructField) (name, idCol, typeCol, value string, ok bool) {
	var m map[string]string
	if o, has := ormRelOpts(sf); has {
		m = o
	} else if tag, has := sf.Tag.Lookup("gorm"); has {
		m = scan.ParseGormTag(tag)
	}
	name = firstNonEmpty(m["polymorphic"], m["poly"])
	if name == "" {
		return "", "", "", "", false
	}
	return name, m["polymorphicid"], m["polymorphictype"], m["polymorphicvalue"], true
}

// relWantsConstraint reports whether a relation field opts into a FOREIGN KEY
// constraint via a `constraint:` tag (gorm spelling read too). The value is not
// interpreted — its presence is the opt-in.
func relWantsConstraint(sf reflect.StructField) bool {
	if o, ok := ormRelOpts(sf); ok {
		_, has := o["constraint"]
		return has
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		_, has := scan.ParseGormTag(tag)["constraint"]
		return has
	}
	return false
}

// relTagOverride reads fk/references overrides from a relation field's tags. The
// values may be column names or Go field names (resolved later via resolveCol).
func relTagOverride(sf reflect.StructField) (fk, references string) {
	if o, ok := ormRelOpts(sf); ok {
		fk = firstNonEmpty(o["fk"], o["foreignkey"])
		references = firstNonEmpty(o["references"], o["ref"])
		return fk, references
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		g := scan.ParseGormTag(tag)
		fk = g["foreignkey"]
		references = g["references"]
	}
	return fk, references
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
