package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// TestORMUpsert covers orm.Repo.Upsert: insert, then a conflicting upsert updates
// in place (no duplicate), in one statement.
func TestORMUpsert(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[Language](DB)

	if err := repo.Upsert(ctx, &Language{Code: "orm-up", Name: "First"}, query.OnConflict("code").DoUpdate("name")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, &Language{Code: "orm-up", Name: "Second"}, query.OnConflict("code").DoUpdate("name")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Where("code = ?", "orm-up").Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Second" {
		t.Errorf("orm Upsert = %+v, want one row named Second", got)
	}
}

// TestGetByKeys covers batch get by a list of primary keys.
func TestGetByKeys(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	var ids []int64
	for range 3 {
		u := &User{Name: "gbk"}
		mustCreate(t, u)
		ids = append(ids, u.ID)
	}

	got, err := repo.GetByKeys(ctx, ids[0], ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("GetByKeys returned %d, want 2", len(got))
	}

	// Empty key list is an empty result, not an error.
	if rows, err := repo.GetByKeys(ctx); err != nil || len(rows) != 0 {
		t.Errorf("GetByKeys() = %v rows, err=%v, want 0/nil", len(rows), err)
	}

	// Soft-delete one: it drops out of the default scope.
	mid := &User{ID: ids[1]}
	if err := repo.Delete(ctx, mid); err != nil {
		t.Fatal(err)
	}
	if rows, _ := repo.GetByKeys(ctx, ids[0], ids[1], ids[2]); len(rows) != 2 {
		t.Errorf("GetByKeys after soft-delete = %d, want 2 (deleted one excluded)", len(rows))
	}

	// Composite-PK model is rejected.
	if _, err := orm.NewRepo[Membership](DB).GetByKeys(ctx, 1, 2); err == nil {
		t.Error("GetByKeys on a composite-PK model should error")
	}
}

// TestRestore covers un-soft-deleting a row.
func TestRestore(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "restore-me"}
	mustCreate(t, u)
	if err := repo.Delete(ctx, u); err != nil { // soft delete
		t.Fatal(err)
	}
	if ok, _ := repo.Where("name = ?", "restore-me").Exists(ctx); ok {
		t.Fatal("soft-deleted row should be hidden from the default scope")
	}

	if err := repo.Restore(ctx, u); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ok, _ := repo.Where("name = ?", "restore-me").Exists(ctx); !ok {
		t.Error("restored row should be visible again")
	}
	if u.DeletedAt.Valid {
		t.Error("Restore should clear DeletedAt in memory too")
	}

	// Restoring an already-live row matches nothing under no-op? It still matches by
	// PK (the soft-delete column is already NULL), so it succeeds (1 row). A model
	// without a soft-delete column is a clear error.
	if err := orm.NewRepo[Company](DB).Restore(ctx, &Company{ID: 1}); err == nil {
		t.Error("Restore on a model without a soft-delete column should error")
	}

	// Restore of a non-existent row is ErrNoRows.
	if err := repo.Restore(ctx, &User{ID: 9_000_002}); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("Restore(missing) = %v, want ErrNoRows", err)
	}
}
