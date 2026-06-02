package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

func TestPreloadNestedPath(t *testing.T) {
	ctx := context.Background()
	// user → Manager (belongs-to self) → Company (belongs-to) — a two-level path.
	co := &Company{Name: "preload-co"}
	mustCreate(t, co)
	mgr := &User{Name: "preload-mgr", CompanyID: co.ID}
	mustCreate(t, mgr)
	u := &User{Name: "preload-u", ManagerID: mgr.ID}
	mustCreate(t, u)

	list := fresh(t, u.ID)
	if err := orm.LoadPath[User](ctx, DB, list, "Manager.Company"); err != nil {
		t.Fatal(err)
	}
	got := list[0]
	if got.Manager == nil || got.Manager.Company == nil || got.Manager.Company.Name != "preload-co" {
		t.Errorf("nested Manager.Company not loaded: %+v", got.Manager)
	}
}

func TestPreloaderMultiplePaths(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "preload-multi", Config{Account: true, Pets: 2, Languages: 2})
	list := fresh(t, u.ID)
	if err := orm.NewPreloader[User](DB).
		With("Account").
		With("Pets").
		With("Languages").
		Load(ctx, list); err != nil {
		t.Fatal(err)
	}
	got := list[0]
	if got.Account == nil {
		t.Error("Account not preloaded")
	}
	if len(got.Pets) != 2 {
		t.Errorf("Pets preloaded = %d, want 2", len(got.Pets))
	}
	if len(got.Languages) != 2 {
		t.Errorf("Languages preloaded = %d, want 2", len(got.Languages))
	}
}
