package conformance_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/migrate"
	"liteorm.org/orm"
)

// Gadget's model has a price column the live table will initially lack, so the
// diff produces an additive ALTER.
type Gadget struct {
	ID    int64
	Name  string
	Price int64
}

func (Gadget) TableName() string { return "gadgets" }

// TestEmitMigrationRoundTrip closes the loop the two-track migration story
// promises: a model gains a column, GenerateMigration emits reviewable up/down
// SQL, WritePair writes a golang-migrate pair, and the runner Loads and applies
// it — after which the model no longer diffs against the database.
func TestEmitMigrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "emit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The live table is missing Gadget.Price.
	if _, err := db.ExecContext(ctx, `CREATE TABLE gadgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	up, down, err := orm.GenerateMigration[Gadget](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(up, "ADD COLUMN") || !strings.Contains(up, "price") {
		t.Fatalf("up migration should add the price column, got:\n%s", up)
	}

	dir := t.TempDir()
	if _, _, err := migrate.WritePair(dir, 1, "add widget price", up, down); err != nil {
		t.Fatal(err)
	}

	migs, err := migrate.Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	n, err := migrate.New(db).Up(ctx, migs)
	if err != nil {
		t.Fatalf("apply generated migration: %v", err)
	}
	if n != 1 {
		t.Fatalf("applied %d migrations, want 1", n)
	}

	// The price column now exists, and the model no longer diffs.
	cols, err := orm.IntrospectColumns(ctx, db, "gadgets")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cols {
		if c.Name == "price" {
			found = true
		}
	}
	if !found {
		t.Fatalf("price column missing after migration; columns=%v", cols)
	}
	ch, err := orm.Diff[Gadget](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Empty() {
		t.Fatalf("model still diffs after applying generated migration: %+v", ch)
	}
}
