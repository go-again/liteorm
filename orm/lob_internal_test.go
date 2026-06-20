package orm

import (
	"context"
	"strings"
	"testing"

	liteorm "liteorm.org"
)

type lobModel struct {
	ID   int64 `orm:"id,pk"`
	Blob LOB   `lob:"chunk=1m"`
}

func (lobModel) TableName() string { return "lobmodels" }

func TestSchemaDetectsLOBField(t *testing.T) {
	s, err := SchemaOf[lobModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LOBFields) != 1 {
		t.Fatalf("LOBFields = %d, want 1", len(s.LOBFields))
	}
	lf := s.LOBFields[0]
	if lf.GoName != "Blob" || lf.Column != "blob" || lf.ChunkSize != 1<<20 {
		t.Errorf("LOBField = %+v, want {Blob blob ... 1048576}", lf)
	}
	if lf.Compression != CompressionNone {
		t.Errorf("Compression = %d, want CompressionNone (no compress tag)", lf.Compression)
	}
	// The LOB column is still an ordinary column in the schema (it holds the id).
	found := false
	for _, f := range s.Fields {
		if f.Column == "blob" {
			found = true
		}
	}
	if !found {
		t.Error("blob column missing from schema.Fields")
	}
}

type lobCompressModel struct {
	ID   int64 `orm:"id,pk"`
	Blob LOB   `lob:"chunk=64k;compress=best"`
}

func (lobCompressModel) TableName() string { return "lobcompressmodels" }

func TestSchemaDetectsCompression(t *testing.T) {
	s, err := SchemaOf[lobCompressModel]()
	if err != nil {
		t.Fatal(err)
	}
	lf := s.LOBFields[0]
	if lf.ChunkSize != 64<<10 || lf.Compression != CompressionBest {
		t.Errorf("LOBField = %+v, want chunk 65536 + CompressionBest", lf)
	}
}

func TestParseCompression(t *testing.T) {
	cases := []struct {
		v      string
		hasVal bool
		want   Compression
	}{
		{"", false, CompressionDefault}, // bare `compress`, no '='
		{"", true, CompressionDefault},  // `compress=`
		{"default", true, CompressionDefault},
		{"none", true, CompressionNone},
		{"fastest", true, CompressionFastest},
		{"fast", true, CompressionFast},
		{"better", true, CompressionBetter},
		{"BEST", true, CompressionBest}, // case-insensitive
	}
	for _, c := range cases {
		got, err := parseCompression(c.v, c.hasVal)
		if err != nil || got != c.want {
			t.Errorf("parseCompression(%q, %v) = %d, %v; want %d", c.v, c.hasVal, got, err, c.want)
		}
	}
	// Undocumented boolean aliases are rejected — the grammar matches the docs.
	for _, bad := range []string{"turbo", "on", "off", "true", "yes"} {
		if _, err := parseCompression(bad, true); err == nil {
			t.Errorf("compress=%s should error (not a documented level)", bad)
		}
	}
}

func TestParseByteSize(t *testing.T) {
	for in, want := range map[string]int{"64k": 64 << 10, "1m": 1 << 20, "512": 512, "2G": 2 << 30} {
		got, err := parseByteSize(in)
		if err != nil || got != want {
			t.Errorf("parseByteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseByteSize("nope"); err == nil {
		t.Error("a non-numeric size should error")
	}
}

func TestProvisionLOBsWithoutProvisioner(t *testing.T) {
	// Nothing in the orm test binary imports dialect/sqlite/lob, so the provisioner
	// is unregistered — a LOB model must fail AutoMigrate loudly, pointing at the
	// missing import rather than silently skipping the content store.
	if lobProvisioner != nil {
		t.Skip("a LOB provisioner is registered in this binary")
	}
	s, _ := SchemaOf[lobModel]()
	err := provisionLOBs(context.Background(), nil, s, migrateConfig{})
	if err == nil || !strings.Contains(err.Error(), "dialect/sqlite/lob") {
		t.Fatalf("want a missing-provisioner error naming the import, got %v", err)
	}
}

// TestProvisionLOBsOverride proves a WithLOB* override wins over the tag and
// merges per knob: WithLOBCompression overrides compression while the tag's chunk
// size is kept untouched.
func TestProvisionLOBsOverride(t *testing.T) {
	var got LOBProvisionOptions
	var gotStore string
	prev := lobProvisioner
	lobProvisioner = func(_ context.Context, _ liteorm.Session, store string, opts LOBProvisionOptions) error {
		gotStore, got = store, opts
		return nil
	}
	defer func() { lobProvisioner = prev }()

	s, _ := SchemaOf[lobModel]() // Blob: lob:"chunk=1m", no compression
	cfg := buildMigrateConfig([]MigrateOption{WithLOBCompression("Blob", CompressionBest)})
	if err := provisionLOBs(context.Background(), nil, s, cfg); err != nil {
		t.Fatal(err)
	}
	if got.ChunkSize != 1<<20 {
		t.Errorf("ChunkSize = %d, want 1m kept from the tag (compression override must not touch it)", got.ChunkSize)
	}
	if got.Compression != CompressionBest {
		t.Errorf("Compression = %d, want CompressionBest from the override", got.Compression)
	}
	if gotStore != "lobmodels_blob" {
		t.Errorf("store = %q, want lobmodels_blob", gotStore)
	}
}

// TestWithLOBOptionValidation confirms a bad override value (the kind a CLI flag
// could supply) errors at AutoMigrate rather than silently degrading to a default
// — surfaced before any DDL, so sess is never touched.
func TestWithLOBOptionValidation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opt  MigrateOption
		want string
	}{
		{"zero chunk", WithLOBChunkSize("Blob", 0), "must be positive"},
		{"negative chunk", WithLOBChunkSize("Blob", -1), "must be positive"},
		{"out-of-range compression", WithLOBCompression("Blob", Compression(99)), "unknown compression"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := AutoMigrate[lobModel](ctx, nil, tc.opt)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AutoMigrate err = %v, want containing %q", err, tc.want)
			}
		})
	}
	// CompressionNone is a valid override (turn a tag's compression off).
	if err := buildMigrateConfig([]MigrateOption{WithLOBCompression("Blob", CompressionNone)}).optErr; err != nil {
		t.Errorf("WithLOBCompression(CompressionNone) should be valid, got %v", err)
	}
}

// TestProvisionLOBsUnknownOverrideField confirms a WithLOB* override naming a
// field that is not an orm.LOB field fails loudly (a typo doesn't silently no-op).
func TestProvisionLOBsUnknownOverrideField(t *testing.T) {
	prev := lobProvisioner
	lobProvisioner = func(context.Context, liteorm.Session, string, LOBProvisionOptions) error { return nil }
	defer func() { lobProvisioner = prev }()

	s, _ := SchemaOf[lobModel]()
	cfg := buildMigrateConfig([]MigrateOption{WithLOBChunkSize("Nonexistent", 4096)})
	if err := provisionLOBs(context.Background(), nil, s, cfg); err == nil || !strings.Contains(err.Error(), "Nonexistent") {
		t.Fatalf("want an unknown-field error naming the bad field, got %v", err)
	}
}
