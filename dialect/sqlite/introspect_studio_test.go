package sqlite_test

import (
	"context"
	"testing"

	sqlite "liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

type isWidget struct {
	ID    int64  `orm:"id,pk"`
	Name  string `orm:"name,notnull"`
	Color string `orm:"color,default:#ffffff"`
	Note  string `orm:"note"`
}

func (isWidget) TableName() string { return "is_widgets" }

func TestIntrospectTablesAndColumnsFull(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := orm.AutoMigrate[isWidget](ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE plain (a INTEGER, b TEXT)"); err != nil {
		t.Fatal(err)
	}

	// Table listing
	tables, err := orm.IntrospectTables(ctx, db)
	if err != nil {
		t.Fatalf("IntrospectTables: %v", err)
	}
	got := map[string]bool{}
	for _, n := range tables {
		got[n] = true
	}
	for _, want := range []string{"is_widgets", "plain"} {
		if !got[want] {
			t.Errorf("table %q missing from %v", want, tables)
		}
	}

	// Full column metadata
	cols, err := orm.IntrospectColumnsFull(ctx, db, "is_widgets")
	if err != nil {
		t.Fatalf("IntrospectColumnsFull: %v", err)
	}
	by := map[string]orm.ColumnInfo{}
	for _, c := range cols {
		by[c.Name] = c
	}
	if c := by["id"]; c.PKPos != 1 {
		t.Errorf("id PKPos = %d, want 1", c.PKPos)
	}
	if c := by["name"]; !c.NotNull {
		t.Errorf("name should be NOT NULL")
	}
	if c := by["color"]; c.Default == nil || *c.Default != "'#ffffff'" {
		t.Errorf("color default = %v, want \"'#ffffff'\"", c.Default)
	}
	if c := by["note"]; c.NotNull || c.PKPos != 0 || c.Default != nil {
		t.Errorf("note metadata unexpected: %+v", c)
	}
}
