package codec

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"
)

// Built-in codecs, registered under the same names gorm uses for its
// serializers, so a `gorm:"serializer:json"` model works unchanged.
func init() {
	Register("json", jsonCodec{})
	Register("gob", gobCodec{})
	Register("unixtime", unixTimeCodec{})
}

// jsonCodec stores a field as JSON text.
type jsonCodec struct{}

func (jsonCodec) Encode(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil // a string binds to a TEXT column; []byte would bind as BLOB
}

func (jsonCodec) Decode(src any, dst any) error {
	b := toBytes(src)
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dst)
}

func (jsonCodec) StorageKind() StorageKind { return Text }

// gobCodec stores a field as gob-encoded bytes.
type gobCodec struct{}

func (gobCodec) Encode(v any) (any, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (gobCodec) Decode(src any, dst any) error {
	b := toBytes(src)
	if len(b) == 0 {
		return nil
	}
	return gob.NewDecoder(bytes.NewReader(b)).Decode(dst)
}

func (gobCodec) StorageKind() StorageKind { return Blob }

// unixTimeCodec stores a time.Time as a Unix-seconds integer.
type unixTimeCodec struct{}

func (unixTimeCodec) Encode(v any) (any, error) {
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return int64(0), nil
		}
		return t.Unix(), nil
	case *time.Time:
		if t == nil || t.IsZero() {
			return nil, nil
		}
		return t.Unix(), nil
	}
	return nil, fmt.Errorf("codec: unixtime needs a time.Time, got %T", v)
}

func (unixTimeCodec) Decode(src any, dst any) error {
	var sec int64
	switch x := src.(type) {
	case nil:
		return nil
	case int64:
		sec = x
	case float64:
		sec = int64(x)
	default:
		return fmt.Errorf("codec: unixtime needs an integer, got %T", src)
	}
	t := time.Unix(sec, 0).UTC()
	switch p := dst.(type) {
	case *time.Time:
		*p = t
	case **time.Time:
		*p = &t
	default:
		return fmt.Errorf("codec: unixtime dst is %T, want *time.Time", dst)
	}
	return nil
}

func (unixTimeCodec) StorageKind() StorageKind { return Integer }

func toBytes(src any) []byte {
	switch x := src.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}
