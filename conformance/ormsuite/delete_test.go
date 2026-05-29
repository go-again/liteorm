package ormsuite

import (
	"context"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
	"liteorm.org/query"
)

func TestSoftDeleteTriState(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "del-soft"}
	mustCreate(t, u)

	if err := repo.Delete(ctx, u); err != nil { // soft delete
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, u.ID); err != liteorm.ErrNoRows {
		t.Errorf("default Get after soft delete err = %v, want ErrNoRows", err)
	}
	if got, _ := repo.IncludeDeleted().Get(ctx, u.ID); got.ID != u.ID {
		t.Error("IncludeDeleted should see the soft-deleted row")
	}
	if only, _ := repo.OnlyDeleted().Where("name = ?", "del-soft").Find(ctx); len(only) != 1 {
		t.Errorf("OnlyDeleted = %d, want 1", len(only))
	}
}

func TestForceDelete(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "del-force"}
	mustCreate(t, u)

	if err := repo.ForceDelete(ctx, u); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.IncludeDeleted().Where("name = ?", "del-force").Find(ctx); len(got) != 0 {
		t.Errorf("ForceDelete should remove the row entirely, found %d", len(got))
	}
}

func TestMultiRowDeleteBuilder(t *testing.T) {
	ctx := context.Background()
	for range 4 {
		mustCreate(t, &Company{Name: "del-co"})
	}
	// Company has no soft-delete column, so this is a hard delete by condition.
	n, err := query.Delete[Company](DB).Where("name = ?", "del-co").Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("multi-row delete affected %d, want 4", n)
	}

	// A WHERE-less delete is refused.
	if _, err := query.Delete[Company](DB).Exec(ctx); err == nil {
		t.Error("WHERE-less delete should be refused")
	}
}
