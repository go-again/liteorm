package codec_test

import (
	"bytes"
	"testing"
	"time"

	"liteorm.org/codec"
)

func TestBuiltins_Registered(t *testing.T) {
	for _, name := range []string{"json", "gob", "unixtime"} {
		if _, ok := codec.Get(name); !ok {
			t.Errorf("built-in codec %q not registered", name)
		}
	}
	if _, ok := codec.Get("nope"); ok {
		t.Error("Get returned a codec for an unregistered name")
	}
}

func TestJSON_RoundTrip(t *testing.T) {
	c, _ := codec.Get("json")
	type pt struct {
		X, Y int
	}
	enc, err := c.Encode(pt{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := enc.(string); !ok {
		t.Fatalf("json Encode returned %T, want string (so it binds to TEXT)", enc)
	}
	var got pt
	if err := c.Decode(enc, &got); err != nil {
		t.Fatal(err)
	}
	if got != (pt{1, 2}) {
		t.Fatalf("json round-trip = %+v, want {1 2}", got)
	}
	if codec.StorageKindOf(c) != codec.Text {
		t.Error("json should store as Text")
	}
}

func TestGob_RoundTrip(t *testing.T) {
	c, _ := codec.Get("gob")
	enc, err := c.Encode([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := enc.([]byte); !ok {
		t.Fatalf("gob Encode returned %T, want []byte", enc)
	}
	var got []string
	if err := c.Decode(enc, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("gob round-trip = %v", got)
	}
	if codec.StorageKindOf(c) != codec.Blob {
		t.Error("gob should store as Blob")
	}
}

func TestUnixTime_RoundTrip(t *testing.T) {
	c, _ := codec.Get("unixtime")
	now := time.Now().Truncate(time.Second).UTC()
	enc, err := c.Encode(now)
	if err != nil {
		t.Fatal(err)
	}
	if enc.(int64) != now.Unix() {
		t.Fatalf("unixtime Encode = %v, want %d", enc, now.Unix())
	}
	var got time.Time
	if err := c.Decode(enc, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(now) {
		t.Fatalf("unixtime round-trip = %v, want %v", got, now)
	}
	if codec.StorageKindOf(c) != codec.Integer {
		t.Error("unixtime should store as Integer")
	}
}

// TestFunc_TypedEncryption is the ssh2incus shape: a []byte field codec that
// transforms bytes without widening the field type. A trivial XOR stands in for
// real AEAD.
func TestFunc_TypedEncryption(t *testing.T) {
	xor := func(b []byte) ([]byte, error) {
		out := make([]byte, len(b))
		for i, c := range b {
			out[i] = c ^ 0x5a
		}
		return out, nil
	}
	c := codec.Func(xor, xor) // self-inverse
	if codec.StorageKindOf(c) != codec.Blob {
		t.Fatal("a []byte-stored Func codec should be Blob")
	}
	plain := []byte("secret value")
	enc, err := c.Encode(plain)
	if err != nil {
		t.Fatal(err)
	}
	stored := enc.([]byte)
	if bytes.Equal(stored, plain) {
		t.Fatal("Encode left the bytes in plaintext")
	}
	var got []byte
	if err := c.Decode(stored, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decode = %q, want %q", got, plain)
	}
	// Decode tolerates the string form a text driver might hand back.
	var got2 []byte
	if err := c.Decode(string(stored), &got2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got2, plain) {
		t.Fatal("decode from string form failed")
	}
}

// TestFunc_IntegerStored covers a Func codec whose stored type is a non-int64
// integer: the column is INTEGER, a driver hands back int64 (or int32 on a pg
// int4), and Decode must still land it in the narrower stored type.
func TestFunc_IntegerStored(t *testing.T) {
	c := codec.Func(
		func(v int) (int32, error) { return int32(v), nil },
		func(n int32) (int, error) { return int(n), nil },
	)
	if codec.StorageKindOf(c) != codec.Integer {
		t.Fatalf("int32-stored codec should be Integer, got %v", codec.StorageKindOf(c))
	}
	if enc, err := c.Encode(7); err != nil || enc.(int32) != 7 {
		t.Fatalf("encode = %v, %v", enc, err)
	}
	// Decode from the int64 a driver returns for an INTEGER column.
	var got int
	if err := c.Decode(int64(7), &got); err != nil || got != 7 {
		t.Fatalf("decode from int64 = %d, %v; want 7", got, err)
	}
	// And from an int32 (a pg int4).
	var got2 int
	if err := c.Decode(int32(9), &got2); err != nil || got2 != 9 {
		t.Fatalf("decode from int32 = %d, %v; want 9", got2, err)
	}

	// A uint16 stored type, decoded from int64.
	cu := codec.Func(
		func(v uint16) (uint16, error) { return v, nil },
		func(n uint16) (uint16, error) { return n, nil },
	)
	var gu uint16
	if err := cu.Decode(int64(40000), &gu); err != nil || gu != 40000 {
		t.Fatalf("uint16 decode = %d, %v; want 40000", gu, err)
	}
}

func TestFunc_WrongType(t *testing.T) {
	c := codec.Func(func(s string) (string, error) { return s, nil }, func(s string) (string, error) { return s, nil })
	if _, err := c.Encode(42); err == nil {
		t.Error("Encode of the wrong Go type should error")
	}
	var n int
	if err := c.Decode("x", &n); err == nil {
		t.Error("Decode into the wrong dst type should error")
	}
}
