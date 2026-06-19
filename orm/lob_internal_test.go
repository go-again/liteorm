package orm

import (
	"context"
	"strings"
	"testing"
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
	err := provisionLOBs(context.Background(), nil, s)
	if err == nil || !strings.Contains(err.Error(), "dialect/sqlite/lob") {
		t.Fatalf("want a missing-provisioner error naming the import, got %v", err)
	}
}
