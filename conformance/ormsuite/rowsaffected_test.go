package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
)

// TestKeyedWriteNoMatchIsErrNoRows: a keyed Update/Delete that matches no row —
// a wrong PK, or a soft-deleted row that is out of the default scope — returns
// liteorm.ErrNoRows instead of silently succeeding.
func TestKeyedWriteNoMatchIsErrNoRows(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	ghost := &User{ID: 9_000_001, Name: "ghost"}
	if err := repo.Update(ctx, ghost); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("Update(missing PK) = %v, want ErrNoRows", err)
	}
	if err := repo.Delete(ctx, ghost); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("Delete(missing PK) = %v, want ErrNoRows", err)
	}
	if err := repo.ForceDelete(ctx, ghost); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("ForceDelete(missing PK) = %v, want ErrNoRows", err)
	}

	// A live row updates/deletes cleanly (1 row affected → no error).
	u := &User{Name: "raff"}
	mustCreate(t, u)
	u.Age = 5
	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("Update(live row) = %v, want nil", err)
	}
	if err := repo.Delete(ctx, u); err != nil { // soft delete
		t.Fatalf("Delete(live row) = %v, want nil", err)
	}

	// Now soft-deleted: out of the default scope, so a keyed update is ErrNoRows —
	// but IncludeDeleted() can still reach it.
	u.Age = 6
	if err := repo.Update(ctx, u); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("Update(soft-deleted, default scope) = %v, want ErrNoRows", err)
	}
	if err := repo.IncludeDeleted().Update(ctx, u); err != nil {
		t.Errorf("IncludeDeleted().Update(soft-deleted) = %v, want nil", err)
	}
}
