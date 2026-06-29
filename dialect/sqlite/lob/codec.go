package lob

import (
	"fmt"
	"math"

	"gosqlite.org/blobstore"
	"liteorm.org/orm"
)

// Compress compresses b with the same compression the large-object store uses
// (this is byte compression, unrelated to the liteorm.org/codec field codecs) —
// handy for compressing your own small values before storing them in an ordinary
// column, independent of the streaming LOB machinery. The result is
// self-describing (it carries a one-byte compression marker), so Decompress needs
// only it; incompressible input falls back to verbatim, so the output is never
// larger than b plus the marker.
func Compress(b []byte, level orm.Compression) ([]byte, error) {
	data, enc, err := blobstore.Compress(b, toBlobstoreCompression(level))
	if err != nil {
		return nil, err
	}
	if enc < 0 || enc > 255 {
		return nil, fmt.Errorf("lob: compression marker %d out of range", enc)
	}
	out := make([]byte, len(data)+1)
	out[0] = byte(enc)
	copy(out[1:], data)
	return out, nil
}

// Decompress reverses Compress. maxSize caps the decompressed length to guard
// against a decompression bomb when the input is untrusted; a non-positive
// maxSize means no cap. An empty input decompresses to nil.
func Decompress(b []byte, maxSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if maxSize <= 0 {
		// The underlying decoder rejects max==0 and computes max+1 internally, so
		// map "no cap" to MaxInt-1 (overflow-safe; it grows incrementally, never
		// pre-allocating the ceiling).
		maxSize = math.MaxInt - 1
	}
	return blobstore.Decompress(b[1:], int(b[0]), maxSize)
}
