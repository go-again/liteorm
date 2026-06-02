package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
)

// TestCompositePKCRUD exercises the full lifecycle of a model whose primary key
// is the (tenant_id, user_id) pair: every row operation must match on both
// columns, and the pair must be unique.
func TestCompositePKCRUD(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[Membership](DB)

	m := &Membership{TenantID: 7, UserID: 42, Role: "owner"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get needs one value per key column, in declaration order.
	got, err := repo.Get(ctx, m.TenantID, m.UserID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	AssertObjEqual(t, &got, m, "TenantID", "UserID", "Role")

	// A second row sharing only one half of the key is a distinct row.
	other := &Membership{TenantID: 7, UserID: 99, Role: "member"}
	if err := repo.Create(ctx, other); err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	// Update keys on both columns: only the addressed row changes.
	m.Role = "admin"
	if err := repo.Update(ctx, m); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = repo.Get(ctx, m.TenantID, m.UserID)
	if got.Role != "admin" {
		t.Errorf("update role = %q, want admin", got.Role)
	}
	sib, _ := repo.Get(ctx, other.TenantID, other.UserID)
	if sib.Role != "member" {
		t.Errorf("sibling row should be untouched, role = %q", sib.Role)
	}

	// Wrong arity is a clear error, not a silent partial match.
	if _, err := repo.Get(ctx, m.TenantID); err == nil {
		t.Error("Get with one value for a two-column key should error")
	}

	// Delete also matches on both columns.
	if err := repo.Delete(ctx, m); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, m.TenantID, m.UserID); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("deleted row still present: err = %v", err)
	}
	if _, err := repo.Get(ctx, other.TenantID, other.UserID); err != nil {
		t.Errorf("delete removed the wrong row: %v", err)
	}
}
