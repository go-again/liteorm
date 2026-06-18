package orm

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"liteorm.org/dialect"
	"liteorm.org/internal/scan"
)

// SyncMode chooses how a sidecar index is kept current with its base table.
type SyncMode int

const (
	// SyncAuto lets the backend pick: triggers for full-text (the indexed text
	// already lives on the base table, so it is free and catches every write) and
	// hooks for vectors (the embedding need not be duplicated on the base table).
	SyncAuto SyncMode = iota
	// SyncTriggers keeps the sidecar current with SQL triggers, so bulk and raw
	// (non-ORM) writes stay in sync. A vector index in trigger mode stores the
	// embedding as a column on the base table.
	SyncTriggers
	// SyncHooks keeps the sidecar current from the ORM write path only (no base
	// column duplication for vectors); writes that bypass the ORM are not indexed.
	SyncHooks
)

// Metric is a vector distance function.
type Metric int

const (
	// L2 is Euclidean distance (the vec0 default).
	L2 Metric = iota
	// Cosine is cosine distance — the usual choice for normalized embeddings.
	Cosine
	// L1 is Manhattan distance.
	L1
	// Hamming is bit-vector Hamming distance.
	Hamming
)

// SearchIndex declares a search sidecar (full-text or vector) attached to a
// model. It is the single representation that both the struct-tag sugar and the
// SearchIndexes method lower into; the migrator, the sync layer, and the typed
// search helpers all read it. The zero value is not useful — construct one with
// [FullText] or [Vector].
type SearchIndex struct {
	Kind dialect.SearchKind
	// Name is the sidecar table name. Empty means the default: <table>_fts for a
	// full-text index, <table>_vec for a vector index. With more than one index of
	// the same kind on a model, give each an explicit name.
	Name string
	// Fields are the model's Go field names the index covers — the text columns
	// for a full-text index, the single embedding field for a vector index.
	Fields []string
	// Sync selects the synchronization strategy (see [SyncMode]).
	Sync SyncMode

	// Full-text options.
	Tokenizer string    // FTS5 tokenizer, e.g. "porter unicode61" (default "unicode61")
	Prefix    []int     // FTS5 prefix-index lengths
	Detail    string    // FTS5 detail level: "full" (default), "column", or "none"
	Content   string    // FTS5 content mode; only external (the default, "") is supported today
	Weights   []float64 // per-column BM25 weights; len must equal len(Fields) when set

	// Vector options.
	Dim      int    // embedding dimension (required, > 0)
	Metric   Metric // distance function (default Cosine)
	Encoding string // "float32" (default), "int8", or "bit"
}

// FullText declares a full-text (FTS5) index over the named model fields. Refine
// it with the With* methods; for the common single-field case the defaults
// (unicode61 tokenizer, external content, trigger sync) are usually right.
func FullText(fields ...string) SearchIndex {
	return SearchIndex{Kind: dialect.SearchFullText, Fields: fields}
}

// Vector declares a vector (sqlite-vec) index over the named embedding field
// with the given dimension. The default metric is [Cosine]; override with
// [SearchIndex.WithMetric].
func Vector(field string, dim int) SearchIndex {
	return SearchIndex{Kind: dialect.SearchVector, Fields: []string{field}, Dim: dim, Metric: Cosine}
}

// Named overrides the sidecar table name.
func (i SearchIndex) Named(name string) SearchIndex { i.Name = name; return i }

// WithSync sets the synchronization strategy.
func (i SearchIndex) WithSync(m SyncMode) SearchIndex { i.Sync = m; return i }

// WithTokenizer sets the FTS5 tokenizer (full-text only).
func (i SearchIndex) WithTokenizer(t string) SearchIndex { i.Tokenizer = t; return i }

// WithPrefix sets FTS5 prefix-index lengths (full-text only).
func (i SearchIndex) WithPrefix(lengths ...int) SearchIndex { i.Prefix = lengths; return i }

// WithDetail sets the FTS5 detail level (full-text only).
func (i SearchIndex) WithDetail(d string) SearchIndex { i.Detail = d; return i }

// WithWeights sets per-column BM25 weights; the count must match the field count
// (full-text only).
func (i SearchIndex) WithWeights(w ...float64) SearchIndex { i.Weights = w; return i }

// WithMetric sets the distance function (vector only).
func (i SearchIndex) WithMetric(m Metric) SearchIndex { i.Metric = m; return i }

// WithEncoding sets the vector storage encoding (vector only).
func (i SearchIndex) WithEncoding(e string) SearchIndex { i.Encoding = e; return i }

func (m Metric) String() string {
	switch m {
	case L2:
		return "l2"
	case Cosine:
		return "cosine"
	case L1:
		return "l1"
	case Hamming:
		return "hamming"
	}
	return "unknown"
}

func parseMetric(s string) (Metric, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "cosine":
		return Cosine, nil
	case "l2", "euclidean":
		return L2, nil
	case "l1", "manhattan":
		return L1, nil
	case "hamming":
		return Hamming, nil
	}
	return 0, fmt.Errorf("unknown metric %q (want l2, cosine, l1, or hamming)", s)
}

func parseSyncMode(s string) (SyncMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return SyncAuto, nil
	case "triggers", "trigger":
		return SyncTriggers, nil
	case "hooks", "hook":
		return SyncHooks, nil
	}
	return 0, fmt.Errorf("unknown sync mode %q (want triggers or hooks)", s)
}

// SearchIndexer is the optional interface a model implements to declare its
// search indexes in typed Go rather than struct tags. Tag-declared and
// method-declared indexes are merged; on a sidecar-name collision the
// method-declared one wins.
type SearchIndexer interface {
	SearchIndexes() []SearchIndex
}

// resolveSearchIndexes collects a model's search indexes from struct tags and
// from the optional SearchIndexes method, validates them, fills default sidecar
// names, and returns the merged set. Returns nil (no error) for a model with no
// search indexes.
func resolveSearchIndexes(t reflect.Type, s *Schema) ([]SearchIndex, error) {
	valid := structFieldNames(t)

	tagged, err := searchIndexesFromTags(t)
	if err != nil {
		return nil, err
	}

	var method []SearchIndex
	if v, ok := reflect.New(t).Interface().(SearchIndexer); ok {
		method = v.SearchIndexes()
	}

	// Validate + default-name every index, keyed by sidecar name. The method's
	// indexes are applied last so they win on a name collision.
	byName := map[string]SearchIndex{}
	order := []string{}
	add := func(ix SearchIndex, fromMethod bool) error {
		if err := validateSearchIndex(&ix, s.Table, valid); err != nil {
			return err
		}
		if _, dup := byName[ix.Name]; dup && !fromMethod {
			return fmt.Errorf("orm: model %s has two search indexes named %q — give each an explicit name", t.Name(), ix.Name)
		}
		if _, seen := byName[ix.Name]; !seen {
			order = append(order, ix.Name)
		}
		byName[ix.Name] = ix
		return nil
	}
	for _, ix := range tagged {
		if err := add(ix, false); err != nil {
			return nil, err
		}
	}
	for _, ix := range method {
		if err := add(ix, true); err != nil {
			return nil, err
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	out := make([]SearchIndex, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}

// validateSearchIndex checks an index and fills its default sidecar name. table
// is the model's table; valid is the set of the model's Go field names.
func validateSearchIndex(ix *SearchIndex, table string, valid map[string]bool) error {
	if len(ix.Fields) == 0 {
		return fmt.Errorf("orm: search index on %q declares no fields", table)
	}
	for _, f := range ix.Fields {
		if !valid[f] {
			return fmt.Errorf("orm: search index on %q references unknown field %q", table, f)
		}
	}
	switch ix.Kind {
	case dialect.SearchVector:
		if len(ix.Fields) != 1 {
			return fmt.Errorf("orm: vector index on %q must cover exactly one field, got %d", table, len(ix.Fields))
		}
		if ix.Dim <= 0 {
			return fmt.Errorf("orm: vector index on %q.%s needs a positive dim", table, ix.Fields[0])
		}
		switch ix.Encoding {
		case "", "float32", "int8", "bit":
		default:
			return fmt.Errorf("orm: vector index on %q has unknown encoding %q (want float32, int8, or bit)", table, ix.Encoding)
		}
		if ix.Name == "" {
			ix.Name = table + "_vec"
		}
	case dialect.SearchFullText:
		if len(ix.Weights) != 0 && len(ix.Weights) != len(ix.Fields) {
			return fmt.Errorf("orm: full-text index on %q has %d weights for %d fields", table, len(ix.Weights), len(ix.Fields))
		}
		switch ix.Detail {
		case "", "full", "column", "none":
		default:
			return fmt.Errorf("orm: full-text index on %q has unknown detail %q (want full, column, or none)", table, ix.Detail)
		}
		if ix.Name == "" {
			ix.Name = table + "_fts"
		}
	default:
		return fmt.Errorf("orm: search index on %q has unknown kind %d", table, ix.Kind)
	}
	return nil
}

// structFieldNames returns the set of exported, non-relation Go field names of t,
// recursing through embedded structs (matching how columns are walked).
func structFieldNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			if _, emb := scan.EmbeddedInfo(sf); emb {
				et := sf.Type
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct {
					walk(et)
				}
				continue
			}
			out[sf.Name] = true
		}
	}
	walk(t)
	return out
}

// searchIndexesFromTags walks a struct's fields and builds one SearchIndex per
// field tagged with a `vector` or `fts` marker. Tags express the common
// single-field case; combined multi-field indexes use the SearchIndexes method.
func searchIndexesFromTags(t reflect.Type) ([]SearchIndex, error) {
	var out []SearchIndex
	var walk func(reflect.Type) error
	walk = func(t reflect.Type) error {
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			if _, emb := scan.EmbeddedInfo(sf); emb {
				et := sf.Type
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct {
					if err := walk(et); err != nil {
						return err
					}
				}
				continue
			}
			ix, ok, err := searchTagOf(sf)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, ix)
			}
		}
		return nil
	}
	if err := walk(t); err != nil {
		return nil, err
	}
	return out, nil
}

// searchTagOf reads a dedicated `vec:` or `fts:` (alias `fts5:`) struct tag whose
// value is a ;-separated list of key=value options — the same grammar the sibling
// gosqlite gorm plugins use, so a model ports across with no tag changes. ok is
// false when the field carries no search tag. A combined multi-column index uses
// the SearchIndexes method instead.
func searchTagOf(sf reflect.StructField) (SearchIndex, bool, error) {
	vecTag, hasVec := sf.Tag.Lookup("vec")
	ftsTag, hasFTS := sf.Tag.Lookup("fts")
	if !hasFTS {
		ftsTag, hasFTS = sf.Tag.Lookup("fts5") // gosqlite-compatible alias
	}
	switch {
	case hasVec && hasFTS:
		return SearchIndex{}, false, fmt.Errorf("orm: field %q is tagged both vec and fts", sf.Name)
	case hasVec:
		return parseVecTag(sf.Name, vecTag)
	case hasFTS:
		return parseFTSTag(sf.Name, ftsTag)
	}
	return SearchIndex{}, false, nil
}

func parseVecTag(field, tag string) (SearchIndex, bool, error) {
	opts := parseSubOpts(tag)
	dim, err := strconv.Atoi(strings.TrimSpace(opts["dim"]))
	if err != nil {
		return SearchIndex{}, false, fmt.Errorf("orm: field %q vec tag needs dim=N, got %q", field, opts["dim"])
	}
	ix := Vector(field, dim)
	if m, ok := opts["metric"]; ok {
		if ix.Metric, err = parseMetric(m); err != nil {
			return SearchIndex{}, false, fmt.Errorf("orm: field %q: %w", field, err)
		}
	}
	if e := opts["encoding"]; e != "" {
		ix.Encoding = e
	}
	if err := applyCommonSearchOpts(&ix, opts); err != nil {
		return SearchIndex{}, false, fmt.Errorf("orm: field %q: %w", field, err)
	}
	return ix, true, nil
}

func parseFTSTag(field, tag string) (SearchIndex, bool, error) {
	opts := parseSubOpts(tag)
	ix := FullText(field)
	if tk := opts["tokenize"]; tk != "" {
		ix.Tokenizer = strings.ReplaceAll(tk, "+", " ") // gosqlite "porter+unicode61" -> "porter unicode61"
	}
	if d := opts["detail"]; d != "" {
		ix.Detail = d
	}
	if p := opts["prefix"]; p != "" {
		lengths, err := parseIntList(p)
		if err != nil {
			return SearchIndex{}, false, fmt.Errorf("orm: field %q prefix: %w", field, err)
		}
		ix.Prefix = lengths
	}
	if w := opts["weights"]; w != "" {
		ws, err := parseFloatList(w)
		if err != nil {
			return SearchIndex{}, false, fmt.Errorf("orm: field %q weights: %w", field, err)
		}
		ix.Weights = ws
	}
	if err := applyCommonSearchOpts(&ix, opts); err != nil {
		return SearchIndex{}, false, fmt.Errorf("orm: field %q: %w", field, err)
	}
	return ix, true, nil
}

// parseSubOpts parses a ;-separated list of key=value (or bare-key) options into
// a map with lowercased keys.
func parseSubOpts(s string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

func parseFloatList(s string) ([]float64, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	out := make([]float64, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// applyCommonSearchOpts reads option keys shared by both index kinds (table name
// and sync mode).
func applyCommonSearchOpts(ix *SearchIndex, opts map[string]string) error {
	if name := opts["table"]; name != "" {
		ix.Name = name
	}
	if s, ok := opts["sync"]; ok {
		m, err := parseSyncMode(s)
		if err != nil {
			return err
		}
		ix.Sync = m
	}
	return nil
}

// parseIntList parses a list of ints separated by spaces or commas (so a prefix
// list reads the same under the comma-delimited orm tag and the
// semicolon-delimited gorm tag).
func parseIntList(s string) ([]int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", f)
		}
		out = append(out, n)
	}
	return out, nil
}
