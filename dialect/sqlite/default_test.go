package sqlite_test

import (
	"context"
	"reflect"
	"testing"

	sqlite "liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// TestSchemaOfType checks the runtime (non-generic) schema resolver added for
// introspection tools that hold models as reflect.Type / interface{}. It must
// return the same cached schema as SchemaOf[T] and dereference pointers.
func TestSchemaOfType(t *testing.T) {
	want, err := orm.SchemaOf[swatch]()
	if err != nil {
		t.Fatal(err)
	}
	got, err := orm.SchemaOfType(reflect.TypeFor[*swatch]())
	if err != nil {
		t.Fatalf("SchemaOfType: %v", err)
	}
	if got != want {
		t.Errorf("SchemaOfType returned a different schema than SchemaOf[T]")
	}
	if got.Table != "swatches" {
		t.Errorf("table = %q, want swatches", got.Table)
	}
	if _, err := orm.SchemaOfType(reflect.TypeFor[int]()); err == nil {
		t.Error("SchemaOfType(int) should error")
	}
}

// TestStats checks the portable pool-stats accessor on the SQLite backend.
func TestStats(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stats, ok := db.Stats()
	if !ok {
		t.Fatal("expected SQLite backend to expose Stats()")
	}
	if stats.OpenConnections < 0 || stats.InUse < 0 {
		t.Errorf("nonsensical stats: %+v", stats)
	}
}

// swatch exercises bare string defaults the way models ported from gorm carry
// them: an identifier-like default (default:user) and one with characters that
// are invalid unquoted in DDL (default:#6c5ce7).
type swatch struct {
	ID    int64  `orm:"id,pk"`
	Role  string `gorm:"default:user"`
	Color string `gorm:"size:7;default:#6c5ce7"`
}

func (swatch) TableName() string { return "swatches" }

func TestAutoMigrateStringDefault(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := orm.AutoMigrate[swatch](ctx, db); err != nil {
		t.Fatalf("AutoMigrate with string defaults: %v", err)
	}

	// Insert relying on the column defaults, then read them back.
	if _, err := db.ExecContext(ctx, "INSERT INTO swatches (id) VALUES (1)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT role, color FROM swatches WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no row")
	}
	var role, color string
	if err := rows.Scan(&role, &color); err != nil {
		t.Fatal(err)
	}
	if role != "user" {
		t.Errorf("role default = %q, want %q", role, "user")
	}
	if color != "#6c5ce7" {
		t.Errorf("color default = %q, want %q", color, "#6c5ce7")
	}
}
