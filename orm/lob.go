package orm

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	liteorm "liteorm.org"
)

// LOB is a large-object column: an INTEGER column that holds a content object's
// id, not the bytes. The row carries only the id (8 bytes); the content itself is
// streamed out-of-band through liteorm.org/dialect/sqlite/lob (Open/Read), never
// materialized with the row — so a multi-GB value never loads into memory. A zero
// LOB is unallocated; the backing object is created on the first write.
//
// Declare it as a field type. AutoMigrate provisions the backing object store (a
// SQLite-only sidecar) when liteorm.org/dialect/sqlite/lob is imported. Tune it
// with a `lob:"..."` tag (semicolon-separated): `chunk=<size>` sets the per-object
// chunk size (e.g. chunk=1m, with a k/m/g suffix) and `compress=<level>` stores
// content compressed (see [Compression]). Both are frozen per object at creation;
// omitted, the backend defaults apply.
type LOB int64

// Allocated reports whether a content object has been created for this field yet
// (a nonzero id). A freshly Created row's LOB is unallocated until first written.
func (l LOB) Allocated() bool { return l != 0 }

// ID returns the underlying object id (0 when unallocated). It is for the
// dialect/sqlite/lob streaming helpers; application code addresses content
// through those, not by the raw id.
func (l LOB) ID() int64 { return int64(l) }

var lobType = reflect.TypeFor[LOB]()

// Compression selects whether (and how hard) a large-object store compresses the
// content it writes. Levels run from fastest (least reduction) to best (most); the
// backend codec is abstracted away. It mirrors the backend's compression levels.
// The mode is frozen per object at allocation, and reads are mode-agnostic — raw
// and compressed objects coexist in one store.
//
// Compression trades CPU and memory for storage: a compressed object cannot use
// in-place incremental I/O, so a partial write read-modify-writes its whole chunk.
// It suits write-once / read-mostly or streamed compressible content (files, logs,
// JSON), not hot random partial updates or already-compressed payloads. Prefer a
// larger chunk size when compressing.
type Compression int

const (
	CompressionNone    Compression = iota // store raw (default)
	CompressionFastest                    // lowest latency, least reduction
	CompressionFast
	CompressionDefault // balanced — the recommended setting when compressing
	CompressionBetter
	CompressionBest // most reduction, slowest
)

// LOBField is a declared large-object column on a model: the base-table column
// holding the object id, plus its provisioning options.
type LOBField struct {
	GoName      string
	Column      string
	Index       []int
	ChunkSize   int         // 0 = backend default
	Compression Compression // CompressionNone = store raw (default)
}

// LOBProvisionOptions are the backend-neutral knobs for provisioning one LOB
// store, passed to the registered provisioner. Carried as a struct so adding a
// knob does not churn the provisioner signature.
type LOBProvisionOptions struct {
	ChunkSize   int
	Compression Compression
}

// lobProvisioner is installed by a backend that can provision object stores
// (liteorm.org/dialect/sqlite/lob, from its init). It is nil on a build that
// never imports such a backend — then a model with a LOB field fails AutoMigrate
// loudly rather than silently skipping the sidecar.
var lobProvisioner func(ctx context.Context, sess liteorm.Session, store string, opts LOBProvisionOptions) error

// RegisterLOBProvisioner installs the object-store provisioner. It is called from
// the init of liteorm.org/dialect/sqlite/lob; applications never call it directly.
func RegisterLOBProvisioner(fn func(ctx context.Context, sess liteorm.Session, store string, opts LOBProvisionOptions) error) {
	lobProvisioner = fn
}

// LOBStoreName is the object-store name for a model's LOB column, "<table>_<column>".
// The dialect/sqlite/lob helpers derive the same name, so a model field maps to a
// stable store across processes.
func LOBStoreName(table, column string) string { return table + "_" + column }

// provisionLOBs creates the backing object store for each declared LOB field,
// idempotently, via the registered provisioner. A model with a LOB field but no
// provisioner registered is a loud error (the dialect/sqlite/lob import is missing).
func provisionLOBs(ctx context.Context, sess liteorm.Session, s *Schema) error {
	if len(s.LOBFields) == 0 {
		return nil
	}
	if lobProvisioner == nil {
		return fmt.Errorf("orm: model %q has a LOB field but no large-object provisioner is registered — import liteorm.org/dialect/sqlite/lob", s.Table)
	}
	for _, lf := range s.LOBFields {
		opts := LOBProvisionOptions{ChunkSize: lf.ChunkSize, Compression: lf.Compression}
		if err := lobProvisioner(ctx, sess, LOBStoreName(s.Table, lf.Column), opts); err != nil {
			return fmt.Errorf("orm: provision LOB %q.%q: %w", s.Table, lf.Column, err)
		}
	}
	return nil
}

// resolveLOBFields collects the model's LOB columns (fields of type orm.LOB) and
// their tag options. It runs after the columns are built, reading each field's
// resolved column name from the schema.
func resolveLOBFields(t reflect.Type, s *Schema) error {
	for _, f := range s.Fields {
		sf := t.FieldByIndex(f.Index)
		if sf.Type != lobType {
			continue
		}
		lf, err := lobOptions(sf)
		if err != nil {
			return err
		}
		lf.Column, lf.Index = f.Column, f.Index
		s.LOBFields = append(s.LOBFields, lf)
	}
	return nil
}

// lobOptions parses a field's `lob:"chunk=...;..."` tag. A missing tag yields zero
// options (backend defaults).
func lobOptions(sf reflect.StructField) (LOBField, error) {
	lf := LOBField{GoName: sf.Name}
	tag, ok := sf.Tag.Lookup("lob")
	if !ok {
		return lf, nil
	}
	for opt := range strings.SplitSeq(tag, ";") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		k, v, hasVal := strings.Cut(opt, "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "chunk":
			n, err := parseByteSize(strings.TrimSpace(v))
			if err != nil {
				return lf, fmt.Errorf("orm: LOB field %q: bad chunk size %q: %w", sf.Name, v, err)
			}
			lf.ChunkSize = n
		case "compress":
			c, err := parseCompression(strings.TrimSpace(v), hasVal)
			if err != nil {
				return lf, fmt.Errorf("orm: LOB field %q: bad compress level %q: %w", sf.Name, v, err)
			}
			lf.Compression = c
		default:
			return lf, fmt.Errorf("orm: LOB field %q: unknown lob option %q", sf.Name, k)
		}
	}
	return lf, nil
}

// parseByteSize parses a byte count with an optional k/m/g (1024-based) suffix.
func parseByteSize(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	mult := 1
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1<<30, s[:len(s)-1]
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n * mult, nil
}

// parseCompression maps a `compress` tag value to a Compression level. A bare
// `compress` (or `compress=`) means the balanced default; a named level picks it
// explicitly. hasVal distinguishes `compress` (no '=') from `compress=`. The
// accepted grammar matches the documented levels exactly — no boolean aliases.
func parseCompression(v string, hasVal bool) (Compression, error) {
	if !hasVal {
		return CompressionDefault, nil
	}
	switch strings.ToLower(v) {
	case "", "default":
		return CompressionDefault, nil
	case "none":
		return CompressionNone, nil
	case "fastest":
		return CompressionFastest, nil
	case "fast":
		return CompressionFast, nil
	case "better":
		return CompressionBetter, nil
	case "best":
		return CompressionBest, nil
	default:
		return CompressionNone, fmt.Errorf("want none|fastest|fast|default|better|best")
	}
}
