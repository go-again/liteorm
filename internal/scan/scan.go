// Package scan maps result rows to typed Go values. It caches a per-T scan plan
// (computed once, reused) and adds the two things generics make possible: no
// interface{} dest, and iter.Seq2[T, error] streaming.
//
// Scanning uses direct field-address scanning (rows.Scan into &struct.Field) for
// correctness and simplicity.
package scan

import (
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Rows is the minimal row cursor scan needs. liteorm.Rows satisfies it.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Columns() ([]string, error)
	Close() error
	Err() error
}

type field struct {
	index []int
	db    string
	pk    bool
	auto  bool
	kind  reflect.Kind
}

type plan struct {
	typ    reflect.Type
	fields []field
	byDB   map[string]int // db column name -> index into fields
}

var cache sync.Map // reflect.Type -> *plan

func planFor[T any]() *plan {
	return planForType(reflect.TypeFor[T]())
}

// planForType returns the cached scan plan for a struct type known only at
// runtime. It backs the reflection-based loaders (nested eager load) that cannot
// name the target as a type parameter.
func planForType(t reflect.Type) *plan {
	if p, ok := cache.Load(t); ok {
		return p.(*plan)
	}
	p := buildPlan(t)
	actual, _ := cache.LoadOrStore(t, p)
	return actual.(*plan)
}

func buildPlan(t reflect.Type) *plan {
	p := &plan{typ: t, byDB: map[string]int{}}
	var walk func(rt reflect.Type, prefix []int, colPrefix string)
	walk = func(rt reflect.Type, prefix []int, colPrefix string) {
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() {
				continue
			}
			ft := sf.Type
			idx := append(append([]int{}, prefix...), i)
			// Flatten embedded structs (anonymous, or named+`embedded`) with prefix.
			if ep, emb := EmbeddedInfo(sf); emb {
				et := ft
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				walk(et, idx, colPrefix+ep)
				continue
			}
			// Association fields are not scalar columns; the orm handles them.
			if IsRelationField(ft) {
				continue
			}
			ci := ResolveColumn(sf)
			if ci.Skip {
				continue
			}
			name := colPrefix + ci.Name
			p.byDB[name] = len(p.fields)
			p.fields = append(p.fields, field{index: idx, db: name, kind: ft.Kind(), pk: ci.PK, auto: ci.Auto})
		}
	}
	walk(t, nil, "")
	return p
}

// All collects every row of rows as a typed T. It closes rows.
func All[T any](rows Rows) ([]T, error) {
	defer func() { _ = rows.Close() }()
	p := planFor[T]()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	dest := make([]any, len(cols))
	var skip any
	var out []T
	for rows.Next() {
		var v T
		if err := scanRow(p, rows, reflect.ValueOf(&v).Elem(), cols, dest, &skip); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Iter streams rows as typed T values, lazily and early-stoppable. It streams
// rows lazily and closes them when the loop ends.
func Iter[T any](rows Rows) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		defer func() { _ = rows.Close() }()
		p := planFor[T]()
		cols, err := rows.Columns()
		if err != nil {
			var z T
			yield(z, err)
			return
		}
		dest := make([]any, len(cols))
		var skip any
		for rows.Next() {
			var v T
			if err := scanRow(p, rows, reflect.ValueOf(&v).Elem(), cols, dest, &skip); err != nil {
				if !yield(v, err) {
					return
				}
				continue
			}
			if !yield(v, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			var z T
			yield(z, err)
		}
	}
}

// Scalars collects a single-column result as []V — the backing for query.Pluck.
// It scans through scanDest, so a bool column from a driver that returns an
// integer (modernc SQLite) still lands in a bool V. It closes rows.
func Scalars[V any](rows Rows) ([]V, error) {
	defer func() { _ = rows.Close() }()
	var out []V
	for rows.Next() {
		var v V
		if err := rows.Scan(scanDest(reflect.ValueOf(&v).Elem())); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Into scans the current row of rows into *v. The caller controls Next/Close.
// Used to read an INSERT ... RETURNING row back into the model.
func Into[T any](rows Rows, v *T) error {
	p := planFor[T]()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	var skip any
	return scanRow(p, rows, reflect.ValueOf(v).Elem(), cols, make([]any, len(cols)), &skip)
}

// scanRow binds each column of the current row to a field of rv and scans. The
// caller owns dest (len == len(cols)) and a single skip target, both reused across
// rows so a multi-row read allocates the binding slice once, not per row. dest is
// fully overwritten each call; an unmapped column points at the shared skip (its
// scanned value is discarded, so sharing one target across columns/rows is safe).
func scanRow(p *plan, rows Rows, rv reflect.Value, cols []string, dest []any, skip *any) error {
	for i, c := range cols {
		if fi, ok := p.byDB[c]; ok {
			dest[i] = scanDest(fieldByIndexAlloc(rv, p.fields[fi].index))
		} else {
			dest[i] = skip
		}
	}
	return rows.Scan(dest...)
}

// scanDest returns a scan target for a field, wrapping bool fields so a driver
// that returns an integer (e.g. SQLite over modernc, which has no bool type)
// still scans into them.
func scanDest(fv reflect.Value) any {
	if fv.Kind() == reflect.Bool || (fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.Bool) {
		return boolScanner{v: fv}
	}
	return fv.Addr().Interface()
}

// boolScanner adapts an integer/bool/text driver value into a bool (or *bool) field.
type boolScanner struct{ v reflect.Value }

func (b boolScanner) Scan(src any) error {
	if src == nil {
		if b.v.Kind() == reflect.Pointer {
			b.v.SetZero()
		} else {
			b.v.SetBool(false)
		}
		return nil
	}
	var bv bool
	switch x := src.(type) {
	case bool:
		bv = x
	case int64:
		bv = x != 0
	case float64:
		bv = x != 0
	case []byte:
		bv = len(x) == 1 && x[0] == '1' || string(x) == "true"
	case string:
		bv = x == "1" || x == "true"
	default:
		return fmt.Errorf("scan: cannot convert %T to bool", src)
	}
	if b.v.Kind() == reflect.Pointer {
		nb := reflect.New(b.v.Type().Elem())
		nb.Elem().SetBool(bv)
		b.v.Set(nb)
	} else {
		b.v.SetBool(bv)
	}
	return nil
}

// AllReflect collects every row as a value of struct type elem, returning a
// reflect.Value of kind slice ([]elem). It is the reflection twin of All, used by
// the orm's nested loader where the target type is only known at runtime. It
// closes rows.
func AllReflect(rows Rows, elem reflect.Type) (reflect.Value, error) {
	defer func() { _ = rows.Close() }()
	p := planForType(elem)
	out := reflect.MakeSlice(reflect.SliceOf(elem), 0, 0)
	cols, err := rows.Columns()
	if err != nil {
		return out, err
	}
	dest := make([]any, len(cols))
	var skip any
	for rows.Next() {
		v := reflect.New(elem)
		if err := scanRow(p, rows, v.Elem(), cols, dest, &skip); err != nil {
			return out, err
		}
		out = reflect.Append(out, v.Elem())
	}
	return out, rows.Err()
}

// AllKeyedReflect is the reflection twin of AllKeyed: it scans rows where keyCol
// carries an association key and the rest map to struct type elem, returning the
// keys aligned with a slice ([]elem) reflect.Value. Used by nested many-to-many
// eager loading. It closes rows.
func AllKeyedReflect(rows Rows, keyCol string, elem reflect.Type) (keys []any, items reflect.Value, err error) {
	defer func() { _ = rows.Close() }()
	p := planForType(elem)
	items = reflect.MakeSlice(reflect.SliceOf(elem), 0, 0)
	cols, err := rows.Columns()
	if err != nil {
		return nil, items, err
	}
	dest := make([]any, len(cols))
	var skip, key any
	for rows.Next() {
		v := reflect.New(elem)
		rv := v.Elem()
		for i, c := range cols {
			switch c {
			case keyCol:
				dest[i] = &key
			default:
				if fi, ok := p.byDB[c]; ok {
					dest[i] = scanDest(fieldByIndexAlloc(rv, p.fields[fi].index))
				} else {
					dest[i] = &skip
				}
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return keys, items, err
		}
		keys = append(keys, key) // append copies the interface value; key is reused next row
		items = reflect.Append(items, rv)
	}
	return keys, items, rows.Err()
}

// Columns returns the db column names of T in field order. When skipAuto is
// true, auto-increment primary-key columns are omitted (for INSERT).
func Columns[T any](skipAuto bool) []string {
	p := planFor[T]()
	cols := make([]string, 0, len(p.fields))
	for _, f := range p.fields {
		if skipAuto && f.auto {
			continue
		}
		cols = append(cols, f.db)
	}
	return cols
}

// Values returns the field values of v for the given db columns, in order.
func Values[T any](v *T, cols []string) []any {
	p := planFor[T]()
	rv := reflect.ValueOf(v).Elem()
	out := make([]any, len(cols))
	for i, c := range cols {
		if fi, ok := p.byDB[c]; ok {
			out[i] = fieldByIndexAlloc(rv, p.fields[fi].index).Interface()
		}
	}
	return out
}

// PrimaryKey returns the db column name of T's first primary-key column, if any.
func PrimaryKey[T any]() (string, bool) {
	p := planFor[T]()
	for _, f := range p.fields {
		if f.pk {
			return f.db, true
		}
	}
	return "", false
}

// PrimaryKeys returns the db column names of T's primary key in declaration order
// (one element for a simple key, more for a composite key).
func PrimaryKeys[T any]() []string {
	p := planFor[T]()
	var out []string
	for _, f := range p.fields {
		if f.pk {
			out = append(out, f.db)
		}
	}
	return out
}

// AutoPrimaryKey returns the column name of T's primary key only when it is a
// single auto-increment column — the only case where an INSERT has a
// database-generated key to read back. A composite key, or a single
// caller-assigned key, returns false.
func AutoPrimaryKey[T any]() (string, bool) {
	p := planFor[T]()
	col, n := "", 0
	for _, f := range p.fields {
		if f.pk {
			n++
			if f.auto {
				col = f.db
			}
		}
	}
	if n == 1 && col != "" {
		return col, true
	}
	return "", false
}

// SetPrimaryKey assigns id into v's integer primary-key field (used after an
// INSERT when the backend returns a LastInsertId rather than RETURNING/OUTPUT).
func SetPrimaryKey[T any](v *T, id int64) {
	p := planFor[T]()
	rv := reflect.ValueOf(v).Elem()
	for _, f := range p.fields {
		if !f.pk {
			continue
		}
		fv := fieldByIndexAlloc(rv, f.index)
		switch {
		case fv.CanInt():
			fv.SetInt(id)
		case fv.CanUint():
			fv.SetUint(uint64(id))
		}
		return
	}
}

func fieldByIndexAlloc(v reflect.Value, index []int) reflect.Value {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v
}

func hasOpt(opts []string, want string) bool {
	return slices.Contains(opts, want)
}

func isIntKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// Snake exposes snake_case conversion for default table-name derivation.
func Snake(s string) string { return toSnake(s) }

// toSnake converts a Go field name to snake_case, treating runs of capitals as
// one word so "UserID" -> "user_id" and "CreatedAt" -> "created_at".
func toSnake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if r >= 'A' && r <= 'Z' {
			prevLower := i > 0 && rs[i-1] >= 'a' && rs[i-1] <= 'z'
			prevDigit := i > 0 && rs[i-1] >= '0' && rs[i-1] <= '9'
			if prevLower || prevDigit {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
