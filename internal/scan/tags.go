package scan

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"slices"
	"strings"
)

// ColumnInfo is the column identity liteorm derives from a struct field's tags.
type ColumnInfo struct {
	Name  string
	PK    bool
	Auto  bool
	Skip  bool
	Codec string // a field codec name (from `codec:`/`serializer:`), empty for none
}

// ResolveColumn derives a field's column identity from its tags, in precedence
// db > orm > gorm, falling back to snake_case. Shared by the scanner and the orm
// schema builder so both front-ends agree on column identity — a gorm-annotated
// model works in `query` and `orm` alike.
func ResolveColumn(sf reflect.StructField) ColumnInfo {
	if tag, ok := sf.Tag.Lookup("db"); ok {
		name, opts := ParseList(tag)
		if name == "-" {
			return ColumnInfo{Skip: true}
		}
		ci := finalize(sf, name, hasOpt(opts, "pk"), hasOpt(opts, "auto") || hasOpt(opts, "autoincrement"), hasOpt(opts, "noauto"))
		ci.Codec = optValue(opts, "codec", "serializer")
		return ci
	}
	if tag, ok := sf.Tag.Lookup("orm"); ok {
		name, opts := ParseList(tag)
		if name == "-" {
			return ColumnInfo{Skip: true}
		}
		pk := hasOpt(opts, "pk") || hasOpt(opts, "primarykey")
		ci := finalize(sf, name, pk, hasOpt(opts, "auto") || hasOpt(opts, "autoincrement"), hasOpt(opts, "noauto"))
		ci.Codec = optValue(opts, "codec", "serializer")
		return ci
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		g := ParseGormTag(tag)
		if _, skip := g["-"]; skip {
			return ColumnInfo{Skip: true}
		}
		_, pk := g["primarykey"]
		_, auto := g["autoincrement"]
		ci := finalize(sf, g["column"], pk, auto, false)
		if s := g["serializer"]; s != "" {
			ci.Codec = s
		} else {
			ci.Codec = g["codec"]
		}
		return ci
	}
	return finalize(sf, "", false, false, false)
}

// optValue returns the value of the first `key:value` option in opts whose key
// (case-insensitive) matches one of keys.
func optValue(opts []string, keys ...string) string {
	for _, o := range opts {
		k, v, ok := strings.Cut(o, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if slices.Contains(keys, k) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func finalize(sf reflect.StructField, name string, pk, explicitAuto, noAuto bool) ColumnInfo {
	if name == "" {
		name = toSnake(sf.Name)
	}
	if !pk && name == "id" {
		pk = true
	}
	auto := explicitAuto || (pk && !noAuto && isIntKind(baseKind(sf.Type)))
	return ColumnInfo{Name: name, PK: pk, Auto: auto}
}

// ParseList splits a comma-separated tag into its first token (the column name)
// and the remaining options.
func ParseList(tag string) (name string, opts []string) {
	if tag == "" {
		return "", nil
	}
	parts := strings.Split(tag, ",")
	return strings.TrimSpace(parts[0]), parts[1:]
}

// ParseGormTag parses a gorm struct tag (`;`-separated KEY or KEY:VALUE, keys
// case-insensitive) into a lowercased-key map. Bare keys map to "".
func ParseGormTag(tag string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(tag, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, hasV := strings.Cut(part, ":")
		key := strings.ToLower(strings.TrimSpace(k))
		if hasV {
			out[key] = strings.TrimSpace(v)
		} else {
			out[key] = ""
		}
	}
	return out
}

func baseKind(t reflect.Type) reflect.Kind {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind()
}

var (
	scannerType = reflect.TypeFor[sql.Scanner]()
	valuerType  = reflect.TypeFor[driver.Valuer]()
)

// IsRelationField reports whether a struct field is an association (a struct, a
// pointer-to-struct, or a slice-of-struct that is not time.Time and does not
// implement sql.Scanner/driver.Valuer) rather than a scalar column. The scanner
// skips these; the orm schema treats them as relations.
func IsRelationField(ft reflect.Type) bool {
	if implementsScannerOrValuer(ft) {
		return false
	}
	base := ft
	for base.Kind() == reflect.Pointer || base.Kind() == reflect.Slice {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return false
	}
	if base.String() == "time.Time" {
		return false
	}
	return true
}

// EmbeddedInfo reports whether a struct field should be flattened into the parent
// (an anonymous struct embed, or a named struct tagged `embedded`), and the column
// prefix to apply (from `embeddedPrefix`). A named struct WITHOUT the tag is a
// relation, not an embed.
func EmbeddedInfo(sf reflect.StructField) (prefix string, embedded bool) {
	base := sf.Type
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct || base.String() == "time.Time" || implementsScannerOrValuer(sf.Type) {
		return "", false
	}
	if tag, ok := sf.Tag.Lookup("orm"); ok {
		var emb bool
		for o := range strings.SplitSeq(tag, ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(o), ":")
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "embedded", "embed":
				emb = true
			case "embeddedprefix", "prefix":
				prefix = strings.TrimSpace(v)
			}
		}
		if emb {
			return prefix, true
		}
	}
	if tag, ok := sf.Tag.Lookup("gorm"); ok {
		g := ParseGormTag(tag)
		if _, ok := g["embedded"]; ok {
			return g["embeddedprefix"], true
		}
	}
	return "", sf.Anonymous
}

func implementsScannerOrValuer(ft reflect.Type) bool {
	pt := reflect.PointerTo(ft)
	return ft.Implements(scannerType) || pt.Implements(scannerType) ||
		ft.Implements(valuerType) || pt.Implements(valuerType)
}
