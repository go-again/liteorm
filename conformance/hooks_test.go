package conformance_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// afModel exercises the AfterFind read hook: it derives a non-persisted field
// from a loaded column.
type afModel struct {
	ID      int64  `orm:"id,pk"`
	Name    string `orm:"name"`
	Display string `orm:"-"` // derived in AfterFind, never stored
}

func (afModel) TableName() string { return "af_models" }

func (m *afModel) AfterFind(_ context.Context, _ *orm.Event[afModel]) error {
	m.Display = "Hello, " + m.Name
	return nil
}

func TestAfterFind_AllReadPaths(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "af.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := orm.AutoMigrate[afModel](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[afModel](db)
	for _, n := range []string{"Ada", "Bo"} {
		if err := repo.Create(ctx, &afModel{Name: n}); err != nil {
			t.Fatal(err)
		}
	}

	// Get
	got, err := repo.Get(ctx, 1)
	if err != nil || got.Display != "Hello, Ada" {
		t.Fatalf("Get: display=%q err=%v", got.Display, err)
	}
	// First
	first, err := repo.OrderBy("id").First(ctx)
	if err != nil || first.Display != "Hello, Ada" {
		t.Fatalf("First: display=%q err=%v", first.Display, err)
	}
	// Find
	all, err := repo.OrderBy("id").Find(ctx)
	if err != nil || len(all) != 2 || all[0].Display != "Hello, Ada" || all[1].Display != "Hello, Bo" {
		t.Fatalf("Find: %+v err=%v", all, err)
	}
	// FindInBatches
	var seen int
	err = repo.FindInBatches(ctx, 1, func(batch []afModel) error {
		for _, m := range batch {
			if m.Display == "" {
				t.Errorf("FindInBatches element not hydrated by AfterFind: %+v", m)
			}
			seen++
		}
		return nil
	})
	if err != nil || seen != 2 {
		t.Fatalf("FindInBatches: seen=%d err=%v", seen, err)
	}

	// A miss does not fire AfterFind (and does not error spuriously).
	if _, err := repo.Get(ctx, 999); !errors.Is(err, liteorm.ErrNoRows) {
		t.Fatalf("Get(miss) = %v, want ErrNoRows", err)
	}
}

// svModel records the order its write hooks fire in.
type svModel struct {
	ID   int64  `orm:"id,pk"`
	Name string `orm:"name"`
}

func (svModel) TableName() string { return "sv_models" }

var svLog []string

func (m *svModel) BeforeSave(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "beforeSave")
	return nil
}
func (m *svModel) BeforeCreate(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "beforeCreate")
	return nil
}
func (m *svModel) AfterCreate(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "afterCreate")
	return nil
}
func (m *svModel) BeforeUpdate(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "beforeUpdate")
	return nil
}
func (m *svModel) AfterUpdate(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "afterUpdate")
	return nil
}
func (m *svModel) AfterSave(context.Context, *orm.Event[svModel]) error {
	svLog = append(svLog, "afterSave")
	return nil
}

func TestSaveHooks_FireOnCreateAndUpdate(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "sv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := orm.AutoMigrate[svModel](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[svModel](db)

	svLog = nil
	m := &svModel{Name: "x"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got := svLog; !slices.Equal(got, []string{"beforeSave", "beforeCreate", "afterCreate", "afterSave"}) {
		t.Fatalf("create hook order = %v", got)
	}

	svLog = nil
	m.Name = "y"
	if err := repo.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got := svLog; !slices.Equal(got, []string{"beforeSave", "beforeUpdate", "afterUpdate", "afterSave"}) {
		t.Fatalf("update hook order = %v", got)
	}
}

// colModel captures the write column set the update hook sees.
type colModel struct {
	ID   int64  `orm:"id,pk"`
	Name string `orm:"name"`
	Age  int64  `orm:"age"`
}

func (colModel) TableName() string { return "col_models" }

var colCaptured []string
var colCreateCaptured []string

func (m *colModel) BeforeUpdate(_ context.Context, ev *orm.Event[colModel]) error {
	colCaptured = append([]string(nil), ev.Columns...)
	return nil
}

func (m *colModel) BeforeCreate(_ context.Context, ev *orm.Event[colModel]) error {
	colCreateCaptured = append([]string(nil), ev.Columns...)
	return nil
}

func TestEventColumns_ReflectSelect(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "col.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := orm.AutoMigrate[colModel](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[colModel](db)
	m := &colModel{Name: "x", Age: 1}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Age = 2
	if err := repo.Select("Age").Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(colCaptured, []string{"age"}) {
		t.Fatalf("ev.Columns under Select(\"Age\") = %v, want [age]", colCaptured)
	}
}

// TestEventColumns_CreatePaths proves ev.Columns is populated (not nil) on the
// create paths that build their own Event — CreateInBatches and Upsert — matching
// the documented "populated on Create and Update" contract.
func TestEventColumns_CreatePaths(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "colcreate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := orm.AutoMigrate[colModel](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[colModel](db)

	colCreateCaptured = nil
	if err := repo.CreateInBatches(ctx, []*colModel{{Name: "a", Age: 1}}, 10); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(colCreateCaptured, []string{"name", "age"}) {
		t.Fatalf("CreateInBatches ev.Columns = %v, want [name age]", colCreateCaptured)
	}

	colCreateCaptured = nil
	if err := repo.Upsert(ctx, &colModel{ID: 99, Name: "b", Age: 2}, query.OnConflict("id").DoUpdate("name")); err != nil {
		t.Fatal(err)
	}
	if len(colCreateCaptured) == 0 {
		t.Fatalf("Upsert ev.Columns = %v, want non-nil", colCreateCaptured)
	}
}

func TestTransaction_CommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := orm.AutoMigrate[afModel](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[afModel](db)

	// commit
	if err := liteorm.Transaction(ctx, db, func(tx *liteorm.BoundTx) error {
		return orm.NewRepo[afModel](tx).Create(ctx, &afModel{Name: "keep"})
	}); err != nil {
		t.Fatal(err)
	}
	// rollback
	boom := errors.New("nope")
	if err := liteorm.Transaction(ctx, db, func(tx *liteorm.BoundTx) error {
		if err := orm.NewRepo[afModel](tx).Create(ctx, &afModel{Name: "drop"}); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Transaction err = %v, want boom", err)
	}

	all, err := repo.Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "keep" {
		t.Fatalf("after commit+rollback, rows = %+v; want only \"keep\"", all)
	}
}
