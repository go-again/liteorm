// Package orm is liteorm's declarative front-end: convention + struct-tag driven
// models over the SAME core (Conn, Tx, dialect, scanner) as the explicit `query`
// front-end, so a value fetched via one feeds the other on the same transaction.
// Its design is generics-first (no reflection/interface{} on the hot path),
// value-oriented (immutable handles, not shared mutable state), and explicit
// (no lazy loading, explicit soft-delete scopes, hard errors instead of silent
// guesses). Models may be annotated with native `orm:"..."` tags or `gorm:"..."`
// tags — both lower into the same schema.
package orm

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
)

// RelKind is the kind of an association.
type RelKind int

const (
	// RelHasMany: the FK lives on the target (e.g. User.Orders []Order, order.user_id).
	RelHasMany RelKind = iota
	// RelBelongsTo: the FK lives on the owner (e.g. User.Company Company, user.company_id).
	RelBelongsTo
	// RelManyToMany: a junction table links owner and target (e.g. User.Roles []Role
	// via user_roles(user_id, role_id)).
	RelManyToMany
	// RelHasOne: a single target whose FK lives on the target (e.g. User.Profile
	// *Profile, profile.user_id). Same shape as has-many but to-one — loaded by the
	// same query direction and assigned as one value.
	RelHasOne
)

// Field is a scalar column in a model schema.
type Field struct {
	GoName     string
	Column     string
	Index      []int
	PK         bool
	Auto       bool
	NotNull    bool
	Unique     bool
	HasIndex   bool   // non-unique secondary index (orm/gorm "index")
	IndexName  string // optional explicit index name
	HasDefault bool
	Default    string
	SoftDelete bool
	AutoCreate bool   // set to now() on Create (autoCreateTime)
	AutoUpdate bool   // set to now() on Create and Update (autoUpdateTime)
	Check      string // CHECK constraint expression, if any
	Readable   bool   // included in SELECT column lists
	Writable   bool   // included in INSERT/UPDATE column lists
	Size       int
	sqlType    string // explicit type override (orm/gorm "type:"), else dialect-derived
	dialField  dialect.Field
}

// Relation is an association. For has-many/belongs-to, OwnerKey/TargetKey are the
// join columns and eager loading runs one batched
// "SELECT * FROM target WHERE TargetKey IN (ownerKeys)". For many-to-many, JoinTable
// links OwnerKey<-OwnerFK and TargetFK->TargetKey, loaded in one JOIN query.
type Relation struct {
	GoName     string
	Kind       RelKind
	Target     reflect.Type
	OwnerKey   string
	TargetKey  string
	Index      []int
	IsSlice    bool
	Constraint bool // belongs-to: emit an FK constraint (opt-in via `constraint:fk`)

	// many-to-many only:
	JoinTable string
	OwnerFK   string // column on JoinTable referencing OwnerKey
	TargetFK  string // column on JoinTable referencing TargetKey

	// polymorphic has-many / has-one only: the target also carries a type column
	// (PolymorphicType) constrained to PolymorphicValue, so one table can be owned
	// by several owner types. TargetKey is then the owner-id column. Empty for a
	// non-polymorphic relation.
	PolymorphicType  string
	PolymorphicValue string
}

// Schema is a model's resolved metadata. The value returned by SchemaOf /
// SchemaOfType is shared and process-cached: read it for introspection (column
// names, LOB options, …), but do not mutate it — that aliases the cache and is
// unsupported. To change LOB chunk size / compression per database, pass
// AutoMigrate's WithLOBChunkSize / WithLOBCompression options instead.
type Schema struct {
	Type       reflect.Type
	Table      string
	Fields     []*Field
	PKs        []*Field // the primary-key columns, in declaration order (composite = >1)
	PK         *Field   // the single PK when there is exactly one; nil for a composite key
	SoftDelete *Field
	Relations  map[string]*Relation
	// SearchIndexes are the model's full-text / vector sidecars, collected from
	// `vector`/`fts` struct tags and the optional SearchIndexes method. Empty for
	// a model with no search indexes.
	SearchIndexes []SearchIndex
	// LOBFields are the model's large-object columns (fields of type orm.LOB),
	// each backed by an out-of-band content store. Empty for a model with none.
	LOBFields []LOBField
}

// WriteColumns returns the columns to write: writable, non-auto-increment, and
// (for updates) non-primary-key. Respects read-only fields (orm "readonly" /
// gorm "<-:false").
func (s *Schema) WriteColumns(forUpdate bool) []string {
	var out []string
	for _, f := range s.Fields {
		if f.Auto || !f.Writable {
			continue
		}
		if forUpdate && f.PK {
			continue
		}
		out = append(out, f.Column)
	}
	return out
}

var schemaCache sync.Map // reflect.Type -> *Schema (or *schemaErr)

type schemaErr struct{ err error }

// SchemaOf returns the resolved schema for T, building and caching it once.
func SchemaOf[T any]() (*Schema, error) {
	return schemaOf(reflect.TypeFor[T]())
}

// SchemaOfType resolves the schema for a model whose type is only known at
// runtime — for tools that hold models as reflect.Type or interface{} (schema
// browsers, generic admin UIs) rather than a type parameter. Pointer types are
// dereferenced to their struct element, so SchemaOfType(reflect.TypeOf(User{}))
// and SchemaOf[User]() return the identical cached *Schema. It errors if t does
// not resolve to a struct.
func SchemaOfType(t reflect.Type) (*Schema, error) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: SchemaOfType requires a struct type, got %v", t)
	}
	return schemaOf(t)
}

func schemaOf(t reflect.Type) (*Schema, error) {
	if v, ok := schemaCache.Load(t); ok {
		if se, ok := v.(*schemaErr); ok {
			return nil, se.err
		}
		return v.(*Schema), nil
	}
	s, err := buildSchema(t)
	if err != nil {
		schemaCache.LoadOrStore(t, &schemaErr{err: err})
		return nil, err
	}
	actual, _ := schemaCache.LoadOrStore(t, s)
	if se, ok := actual.(*schemaErr); ok {
		return nil, se.err
	}
	return actual.(*Schema), nil
}

func buildSchema(t reflect.Type) (*Schema, error) {
	s := &Schema{Type: t, Table: tableName(t), Relations: map[string]*Relation{}}
	walkColumns(t, nil, "", s)
	if len(s.PKs) == 1 {
		s.PK = s.PKs[0] // the single-PK convenience; composite keys leave it nil
	} else if len(s.PKs) > 1 {
		// A composite key is never auto-increment — its parts are caller-assigned.
		// (The bare-`int64`-PK convention marks them auto; undo that for composites.)
		for _, pk := range s.PKs {
			pk.Auto = false
			pk.dialField.AutoIncrement = false
		}
	}
	if err := walkRelations(t, nil, s); err != nil {
		return nil, err
	}
	ix, err := resolveSearchIndexes(t, s)
	if err != nil {
		return nil, err
	}
	s.SearchIndexes = ix
	if err := resolveLOBFields(t, s); err != nil {
		return nil, err
	}
	return s, nil
}

func walkColumns(t reflect.Type, prefix []int, colPrefix string, s *Schema) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		ft := sf.Type
		idx := append(slices.Clone(prefix), i)
		if ep, emb := scan.EmbeddedInfo(sf); emb {
			et := ft
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			walkColumns(et, idx, colPrefix+ep, s)
			continue
		}
		if scan.IsRelationField(ft) {
			continue
		}
		ci := scan.ResolveColumn(sf)
		if ci.Skip {
			continue
		}
		addColumn(sf, idx, colPrefix, ci, s)
	}
}

func addColumn(sf reflect.StructField, idx []int, colPrefix string, ci scan.ColumnInfo, s *Schema) {
	m := readFieldMeta(sf)
	col := colPrefix + ci.Name
	notNull := (m.notNull || ci.PK) && !m.softDelete
	f := &Field{
		GoName: sf.Name, Column: col, Index: idx,
		PK: ci.PK, Auto: ci.Auto, NotNull: notNull, Unique: m.unique,
		HasIndex: m.index, IndexName: m.indexName,
		HasDefault: m.hasDef, Default: m.def, SoftDelete: m.softDelete,
		AutoCreate: m.autoCreate, AutoUpdate: m.autoUpdate, Check: m.check,
		Readable: m.readable, Writable: m.writable, Size: m.size,
		sqlType: m.typeOverride,
		dialField: dialect.Field{
			Name: col, GoType: scan.GoTypeName(sf.Type), PrimaryKey: ci.PK,
			AutoIncrement: ci.Auto, Nullable: !notNull, Size: m.size,
		},
	}
	s.Fields = append(s.Fields, f)
	if f.PK {
		s.PKs = append(s.PKs, f)
	}
	if f.SoftDelete {
		s.SoftDelete = f
	}
}

func walkRelations(t reflect.Type, prefix []int, s *Schema) error {
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		ft := sf.Type
		idx := append(slices.Clone(prefix), i)
		if _, emb := scan.EmbeddedInfo(sf); emb {
			et := ft
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if err := walkRelations(et, idx, s); err != nil {
				return err
			}
			continue
		}
		if !scan.IsRelationField(ft) {
			continue
		}
		rel, err := inferRelation(t, sf, idx, s)
		if err != nil {
			return err
		}
		s.Relations[sf.Name] = rel
	}
	return nil
}

func tableName(t reflect.Type) string { return scan.TableNameOf(t) }

type fieldMeta struct {
	notNull, unique, index, softDelete, hasDef, autoCreate, autoUpdate bool
	readable, writable                                                 bool
	def, typeOverride, check, indexName                                string
	size                                                               int
}

// tagOptions normalizes a field's orm or gorm tag into one lowercased
// key→value option map, so a single switch lowers both into fieldMeta. Note: the
// orm tag is comma-separated, so a value containing a comma (e.g. a multi-part
// `check:`/`default:` expression) is truncated — use the semicolon-separated
// gorm tag form for such values.
func tagOptions(sf reflect.StructField) map[string]string {
	if tag, ok := sf.Tag.Lookup("orm"); ok {
		out := map[string]string{}
		_, opts := scan.ParseList(tag)
		for _, o := range opts {
			k, v, _ := strings.Cut(o, ":")
			out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
		return out
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		return scan.ParseGormTag(tag) // already lowercased keys
	}
	return nil
}

func readFieldMeta(sf reflect.StructField) fieldMeta {
	m := fieldMeta{readable: true, writable: true}
	for k, v := range tagOptions(sf) {
		switch k {
		case "notnull", "not null":
			m.notNull = true
		case "unique", "uniqueindex":
			m.unique = true
		case "index":
			m.index, m.indexName = true, v
		case "soft_delete", "softdelete":
			m.softDelete = true
		case "default":
			m.hasDef, m.def = true, v
		case "type":
			m.typeOverride = v
		case "size":
			m.size = atoi(v)
		case "autocreatetime":
			m.autoCreate = true
		case "autoupdatetime":
			m.autoUpdate = true
		case "check":
			m.check = v
		case "readonly":
			m.writable = false
		case "writeonly":
			m.readable = false
		case "->": // gorm read permission
			if v == "false" {
				m.readable = false
			}
		case "<-": // gorm write permission
			if v == "false" {
				m.writable = false
			}
		}
	}
	return m
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
