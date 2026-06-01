package conformance_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// Nested-load models: Country -has-many-> City -belongs-to-> Mayor.
type Country struct {
	ID     int64
	Name   string
	Cities []City // has-many, fk country_id on City
}

func (Country) TableName() string { return "countries" }

type City struct {
	ID        int64
	CountryID int64 `orm:"country_id"`
	MayorID   int64 `orm:"mayor_id"`
	Name      string
	Mayor     *Mayor // belongs-to, fk mayor_id on City
}

func (City) TableName() string { return "cities" }

type Mayor struct {
	ID   int64
	Name string
}

func (Mayor) TableName() string { return "mayors" }

// Self-referential tree.
type Category struct {
	ID       int64
	ParentID int64 `orm:"parent_id"`
	Name     string
	Children []Category `orm:"fk:parent_id"` // has-many onto itself
}

func (Category) TableName() string { return "categories" }

func openNested(t *testing.T, models ...func(context.Context) error) {
	t.Helper()
	for _, m := range models {
		if err := m(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedLoadPath(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "nested.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	openNested(t,
		func(ctx context.Context) error { return orm.AutoMigrate[Mayor](ctx, db) },
		func(ctx context.Context) error { return orm.AutoMigrate[Country](ctx, db) },
		func(ctx context.Context) error { return orm.AutoMigrate[City](ctx, db) },
	)

	mayors := orm.NewRepo[Mayor](db)
	countries := orm.NewRepo[Country](db)
	cities := orm.NewRepo[City](db)

	usa := Country{Name: "USA"}
	fr := Country{Name: "France"}
	_ = countries.Create(ctx, &usa)
	_ = countries.Create(ctx, &fr)
	ada := Mayor{Name: "Ada"}
	bob := Mayor{Name: "Bob"}
	_ = mayors.Create(ctx, &ada)
	_ = mayors.Create(ctx, &bob)
	_ = cities.Create(ctx, &City{CountryID: usa.ID, MayorID: ada.ID, Name: "NYC"})
	_ = cities.Create(ctx, &City{CountryID: usa.ID, MayorID: bob.ID, Name: "LA"})
	_ = cities.Create(ctx, &City{CountryID: fr.ID, MayorID: ada.ID, Name: "Paris"})

	all, err := countries.Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cs := &countingSession{Session: db}
	if err := orm.LoadPath[Country](ctx, cs, all, "Cities.Mayor"); err != nil {
		t.Fatal(err)
	}
	if cs.queries != 2 {
		t.Errorf("LoadPath used %d queries, want 2 (one per path segment, N+1-safe)", cs.queries)
	}

	byName := map[string]*Country{}
	for i := range all {
		byName[all[i].Name] = &all[i]
	}
	if n := len(byName["USA"].Cities); n != 2 {
		t.Fatalf("USA cities = %d, want 2", n)
	}
	if n := len(byName["France"].Cities); n != 1 {
		t.Fatalf("France cities = %d, want 1", n)
	}
	for _, c := range byName["USA"].Cities {
		if c.Mayor == nil {
			t.Fatalf("city %q: nested Mayor not loaded", c.Name)
		}
		if c.Name == "NYC" && c.Mayor.Name != "Ada" {
			t.Errorf("NYC mayor = %q, want Ada", c.Mayor.Name)
		}
	}

	// The Preloader fluent form runs the same path.
	all2, _ := countries.Find(ctx)
	cs2 := &countingSession{Session: db}
	if err := orm.NewPreloader[Country](cs2).With("Cities.Mayor").Load(ctx, all2); err != nil {
		t.Fatal(err)
	}
	if cs2.queries != 2 {
		t.Errorf("Preloader used %d queries, want 2", cs2.queries)
	}

	// A bad segment is a hard error, never silent.
	if err := orm.LoadPath[Country](ctx, db, all, "Cities.Nope"); err == nil {
		t.Error("LoadPath with an unknown segment should error")
	}
}

func TestSelfReferentialLoadPath(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.AutoMigrate[Category](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[Category](db)

	root := Category{Name: "root"}
	_ = repo.Create(ctx, &root)
	a := Category{ParentID: root.ID, Name: "a"}
	b := Category{ParentID: root.ID, Name: "b"}
	_ = repo.Create(ctx, &a)
	_ = repo.Create(ctx, &b)
	a1 := Category{ParentID: a.ID, Name: "a1"}
	_ = repo.Create(ctx, &a1)

	roots := []Category{root}
	cs := &countingSession{Session: db}
	// Bounded-depth tree load: depth is exactly the number of segments.
	if err := orm.LoadPath[Category](ctx, cs, roots, "Children.Children"); err != nil {
		t.Fatal(err)
	}
	if cs.queries != 2 {
		t.Errorf("two-level tree load used %d queries, want 2", cs.queries)
	}
	if len(roots[0].Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(roots[0].Children))
	}
	var gotA *Category
	for i := range roots[0].Children {
		if roots[0].Children[i].Name == "a" {
			gotA = &roots[0].Children[i]
		}
	}
	if gotA == nil {
		t.Fatal("child 'a' missing")
	}
	if len(gotA.Children) != 1 || gotA.Children[0].Name != "a1" {
		t.Errorf("second-level load did not attach a1 to a: %+v", gotA.Children)
	}
}
