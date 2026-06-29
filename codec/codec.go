// Package codec transparently transforms a struct field on the way to and from
// its database column — JSON-encoding a struct field, gob-encoding a value, or
// encrypting/compressing a []byte — without changing the field's Go type and
// without a wrapper around every read or write.
//
// A codec is attached by name with a struct tag: `orm:"value,codec:json"` (or
// the gorm-compatible `gorm:"serializer:json"`). Because codecs live in
// liteorm's shared scan layer, the transform applies uniformly through BOTH
// front-ends — a column written through the orm and read back through the query
// builder (or vice versa) is encoded and decoded the same way. Built-in codecs
// json, gob, and unixtime are registered automatically; register your own
// (encryption, compression, a domain encoding) with Register, during init,
// before the first query or migration. A field's codec is resolved when its scan
// plan or model schema is first built and then cached, so a codec registered
// after a type has already been read is not picked up — register at init.
//
// A codec decodes the bytes stored in a column back into a Go value, so it
// assumes those bytes are trusted (first-party). Decoding gob from an untrusted
// source is unsafe in particular; do not point a gob (or any) codec at a column
// an untrusted party can write.
package codec

import (
	"fmt"
	"reflect"
	"sync"
)

// StorageKind is how a codec's encoded value is stored, so AutoMigrate can pick
// the column type before any value is written.
type StorageKind int

const (
	Text    StorageKind = iota // a text column (TEXT) — the default
	Blob                       // a binary column (BLOB)
	Integer                    // an integer column (INTEGER)
)

// Codec encodes a Go field value to its stored column representation and back.
// The built-ins return a string ([Text]), []byte ([Blob]), or int64 ([Integer]);
// a custom codec may return any value its driver accepts as a bind argument.
type Codec interface {
	// Encode converts a Go field value to the value stored in the column.
	Encode(v any) (any, error)
	// Decode parses a stored column value into the field. dst is a pointer to
	// the field (a *FieldType). A nil src (a NULL column) is handled by the
	// caller, which zeroes the field, so Decode is not called for it.
	Decode(src any, dst any) error
}

// StorageTyper is an optional Codec extension controlling the migrated column
// type. A codec that does not implement it stores as Text.
type StorageTyper interface {
	StorageKind() StorageKind
}

// StorageKindOf reports c's storage kind (Text unless c is a StorageTyper).
func StorageKindOf(c Codec) StorageKind {
	if st, ok := c.(StorageTyper); ok {
		return st.StorageKind()
	}
	return Text
}

var registry sync.Map // name -> Codec

// Register makes c available under name for the `codec:"<name>"` struct tag (and
// the gorm `serializer:"<name>"` tag). Call it during init, before the first
// query or migration. The last registration for a name wins, so a custom codec
// may override a built-in.
func Register(name string, c Codec) {
	if name == "" || c == nil {
		panic("codec: Register needs a non-empty name and a non-nil codec")
	}
	registry.Store(name, c)
}

// Get returns the codec registered under name.
func Get(name string) (Codec, bool) {
	v, ok := registry.Load(name)
	if !ok {
		return nil, false
	}
	return v.(Codec), true
}

// Func builds a Codec from typed encode/decode functions, so you avoid any-typed
// assertions in your own codec. Go is the field type; Stored is the column
// representation — use string for a TEXT column or []byte for a BLOB column, and
// Func sets the StorageKind to match. A field encryptor is just
// codec.Func(encryptBytes, decryptBytes) over (Go, Stored) = ([]byte, []byte).
func Func[Go any, Stored any](enc func(Go) (Stored, error), dec func(Stored) (Go, error)) Codec {
	return funcCodec[Go, Stored]{enc: enc, dec: dec, kind: storageKindFor[Stored]()}
}

type funcCodec[Go any, Stored any] struct {
	enc  func(Go) (Stored, error)
	dec  func(Stored) (Go, error)
	kind StorageKind
}

func (c funcCodec[Go, Stored]) Encode(v any) (any, error) {
	g, ok := v.(Go)
	if !ok {
		return nil, fmt.Errorf("codec: Encode got %T, want %T", v, *new(Go))
	}
	return c.enc(g)
}

func (c funcCodec[Go, Stored]) Decode(src any, dst any) error {
	p, ok := dst.(*Go)
	if !ok {
		return fmt.Errorf("codec: Decode dst is %T, want *%T", dst, *new(Go))
	}
	s, err := coerce[Stored](src)
	if err != nil {
		return err
	}
	g, err := c.dec(s)
	if err != nil {
		return err
	}
	*p = g
	return nil
}

func (c funcCodec[Go, Stored]) StorageKind() StorageKind { return c.kind }

func storageKindFor[Stored any]() StorageKind {
	switch any(*new(Stored)).(type) {
	case []byte:
		return Blob
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return Integer
	default:
		return Text
	}
}

// coerce adapts a raw driver value into the Stored type a funcCodec expects,
// bridging the representations different drivers return for the same column: a
// TEXT column may scan as string or []byte, and an INTEGER column may scan as
// int64 (SQLite/MySQL) or int32 (a pg int4) regardless of the Stored width. It
// converts via reflection into any numeric / string / []byte Stored kind so a
// `codec.Func` over, say, int32 or a named string type round-trips.
func coerce[Stored any](src any) (Stored, error) {
	var zero Stored
	if src == nil {
		return zero, nil
	}
	if s, ok := src.(Stored); ok { // exact match — no conversion
		return s, nil
	}
	dst := reflect.ValueOf(&zero).Elem()
	sv := reflect.ValueOf(src)
	switch dst.Kind() {
	case reflect.String:
		if sv.Kind() == reflect.Slice && sv.Type().Elem().Kind() == reflect.Uint8 {
			dst.SetString(string(sv.Bytes()))
			return zero, nil
		}
	case reflect.Slice:
		if dst.Type().Elem().Kind() == reflect.Uint8 && sv.Kind() == reflect.String {
			dst.SetBytes([]byte(sv.String()))
			return zero, nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, ok := asInt64(sv); ok {
			dst.SetInt(n)
			return zero, nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, ok := asInt64(sv); ok {
			dst.SetUint(uint64(n))
			return zero, nil
		}
	case reflect.Float32, reflect.Float64:
		if sv.CanFloat() {
			dst.SetFloat(sv.Float())
			return zero, nil
		}
		if n, ok := asInt64(sv); ok {
			dst.SetFloat(float64(n))
			return zero, nil
		}
	}
	return zero, fmt.Errorf("codec: cannot use %T as %T", src, zero)
}

// asInt64 extracts an int64 from any integer/float driver value.
func asInt64(v reflect.Value) (int64, bool) {
	switch {
	case v.CanInt():
		return v.Int(), true
	case v.CanUint():
		return int64(v.Uint()), true
	case v.CanFloat():
		return int64(v.Float()), true
	}
	return 0, false
}
