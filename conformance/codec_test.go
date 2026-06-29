package conformance_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/codec"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// A field encryptor registered for the codec tests. A self-inverse XOR stands in
// for real AEAD — the point is the []byte field is transformed at the persistence
// boundary without widening its Go type (the ssh2incus case).
func init() {
	xor := func(b []byte) ([]byte, error) {
		out := make([]byte, len(b))
		for i, c := range b {
			out[i] = c ^ 0x5a
		}
		return out, nil
	}
	codec.Register("secretbox", codec.Func(xor, xor))
}

type cdcSecret struct {
	ID    int64  `orm:"id,pk"`
	Name  string `orm:"name,unique"`
	Value []byte `orm:"value,codec:secretbox"`
}

func (cdcSecret) TableName() string { return "cdc_secrets" }

type cdcDoc struct {
	ID   int64          `orm:"id,pk"`
	Meta map[string]any `orm:"meta,codec:json"`
}

func (cdcDoc) TableName() string { return "cdc_docs" }

// A gorm-tagged model using gorm's serializer key — proves drop-in compat.
type cdcGormDoc struct {
	ID   int64    `gorm:"column:id;primaryKey"`
	Tags []string `gorm:"column:tags;serializer:json"`
}

func (cdcGormDoc) TableName() string { return "cdc_gdocs" }

type cdcRawSecret struct {
	Value []byte `orm:"value"`
}

type cdcRawDoc struct {
	Meta string `orm:"meta"`
}

// TestFieldCodecs exercises the field-codec seam against live SQLite: encryption
// at the persistence boundary read back through BOTH front-ends, JSON columns,
// gorm serializer compat, the migrated column types, and the loud unknown-codec
// error.
func TestFieldCodecs(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "codec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := orm.AutoMigrateAll(ctx, db, cdcSecret{}, cdcDoc{}, cdcGormDoc{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	// --- ssh2incus case: encrypt a []byte field transparently ---
	plain := []byte("super-secret-token")
	secrets := orm.NewRepo[cdcSecret](db)
	s := &cdcSecret{Name: "api-key", Value: plain}
	if err := secrets.Create(ctx, s); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// On disk the column is ciphertext (read raw, no codec on this type).
	raw, err := query.Raw[cdcRawSecret](ctx, db, "SELECT value FROM cdc_secrets WHERE id = ?", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || bytes.Equal(raw[0].Value, plain) {
		t.Fatalf("stored value is not ciphertext: %q", raw[0].Value)
	}

	// Read back through the orm front-end → plaintext.
	got, err := secrets.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Value, plain) {
		t.Fatalf("orm read = %q, want %q", got.Value, plain)
	}

	// Read back through the query front-end → also plaintext (uniform across both).
	q, err := query.Select[cdcSecret](db).Where("id = ?", s.ID).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(q.Value, plain) {
		t.Fatalf("query read = %q, want %q (codec must apply on both front-ends)", q.Value, plain)
	}

	// --- JSON column ---
	docs := orm.NewRepo[cdcDoc](db)
	d := &cdcDoc{Meta: map[string]any{"k": "v", "n": float64(3)}}
	if err := docs.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	rawDoc, err := query.Raw[cdcRawDoc](ctx, db, "SELECT meta FROM cdc_docs WHERE id = ?", d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawDoc[0].Meta, `"k":"v"`) {
		t.Fatalf("meta not stored as JSON text: %q", rawDoc[0].Meta)
	}
	gotDoc, err := docs.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDoc.Meta["k"] != "v" || gotDoc.Meta["n"] != float64(3) {
		t.Fatalf("json round-trip = %+v", gotDoc.Meta)
	}

	// --- gorm serializer:json compat ---
	gdocs := orm.NewRepo[cdcGormDoc](db)
	g := &cdcGormDoc{Tags: []string{"a", "b"}}
	if err := gdocs.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	gotG, err := gdocs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotG.Tags) != 2 || gotG.Tags[0] != "a" {
		t.Fatalf("gorm serializer round-trip = %v", gotG.Tags)
	}

	// --- migrated column types follow the codec's storage kind ---
	if typ := columnType(t, ctx, db, "cdc_secrets", "value"); typ != "BLOB" {
		t.Errorf("secrets.value column type = %q, want BLOB (encrypt codec stores bytes)", typ)
	}
	if typ := columnType(t, ctx, db, "cdc_docs", "meta"); typ != "TEXT" {
		t.Errorf("docs.meta column type = %q, want TEXT (json codec stores text)", typ)
	}
}

type cdcBad struct {
	ID int64  `orm:"id,pk"`
	X  string `orm:"x,codec:does-not-exist"`
}

func (cdcBad) TableName() string { return "cdc_bads" }

// TestFieldCodec_UnknownErrors proves a model naming an unregistered codec fails
// AutoMigrate loudly, naming the field.
func TestFieldCodec_UnknownErrors(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "badcodec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	err = orm.AutoMigrate[cdcBad](ctx, db)
	if err == nil || !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("AutoMigrate error = %v, want one naming the unregistered codec", err)
	}
}

type cdcPKCodec struct {
	ID   string `orm:"id,pk,codec:json"` // a codec on the identity column — unsupported
	Name string `orm:"name"`
}

func (cdcPKCodec) TableName() string { return "cdc_pk_codecs" }

// TestFieldCodec_PKRejected proves a codec on a primary key fails AutoMigrate —
// identity columns are matched raw, so an encoded key would never round-trip.
func TestFieldCodec_PKRejected(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "pkcodec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	err = orm.AutoMigrate[cdcPKCodec](ctx, db)
	if err == nil || !strings.Contains(err.Error(), "primary key") {
		t.Fatalf("AutoMigrate error = %v, want one rejecting a codec on the PK", err)
	}
}

// columnType returns the declared type of a column via PRAGMA table_info.
func columnType(t *testing.T, ctx context.Context, db *liteorm.DB, table, col string) string {
	t.Helper()
	type info struct {
		Name string `orm:"name"`
		Type string `orm:"type"`
	}
	rows, err := query.Raw[info](ctx, db, "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == col {
			return strings.ToUpper(r.Type)
		}
	}
	t.Fatalf("column %q not found in %q", col, table)
	return ""
}
