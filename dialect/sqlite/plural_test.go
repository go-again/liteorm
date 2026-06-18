package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

type plainWidget struct {
	ID   int64
	Name string
}

// TestPluralTableNames_OrmQueryAgree locks the fix that the plural-table-name
// setting applies to BOTH front-ends: orm.AutoMigrate creates the pluralized
// table and query.Select must read from the same one (not the singular).
func TestPluralTableNames_OrmQueryAgree(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "plural.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	orm.UsePluralTableNames(true)
	t.Cleanup(func() { orm.UsePluralTableNames(false) })

	// orm side: provisions "plain_widgets".
	if err := orm.AutoMigrate[plainWidget](ctx, db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := orm.NewRepo[plainWidget](db).Create(ctx, &plainWidget{Name: "gear"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// query side: if it targeted the singular "plain_widget" it would error with
	// "no such table"; agreement means it reads the row orm wrote.
	got, err := query.Select[plainWidget](db).All(ctx)
	if err != nil {
		t.Fatalf("query.Select under plural naming: %v (orm and query disagree on the table name)", err)
	}
	if len(got) != 1 || got[0].Name != "gear" {
		t.Fatalf("query.Select returned %v, want one widget 'gear'", got)
	}
}
