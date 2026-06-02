package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// Doc is a tenant-scoped row. liteorm has no read/select hook by design, so
// multi-tenant isolation is an explicit reusable scope (OwnedByTenant) on reads
// plus a write-side stamp (BeforeCreate) — the isolation is visible at every call
// site rather than hidden in a global filter.
type Doc struct {
	ID       int64
	TenantID int64 `orm:"tenant_id"`
	Title    string
}

func (Doc) TableName() string { return "docs" }

// BeforeCreate stamps the tenant from context when the caller left it unset.
func (d *Doc) BeforeCreate(ctx context.Context, op *orm.Op[Doc]) error {
	if op.Model.TenantID == 0 {
		if tid, ok := tenantFrom(ctx); ok {
			op.Model.TenantID = tid
		}
	}
	return nil
}

type tenantKey struct{}

// WithTenant carries the current tenant id in the context.
func WithTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

func tenantFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(tenantKey{}).(int64)
	return id, ok
}

// OwnedByTenant is the reusable read scope that confines a query to one tenant.
func OwnedByTenant(id int64) orm.Scope[Doc] {
	return func(b *query.SelectBuilder[Doc]) *query.SelectBuilder[Doc] {
		return b.Filter(query.Col[int64]("tenant_id").Eq(id))
	}
}

func TestMultiTenantIsolation(t *testing.T) {
	const tenantA, tenantB int64 = 100, 200
	docs := orm.NewRepo[Doc](DB)

	// Writes are stamped from the context tenant (no explicit TenantID set).
	ctxA := WithTenant(context.Background(), tenantA)
	for _, title := range []string{"a-spec", "a-notes"} {
		if err := docs.Create(ctxA, &Doc{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	ctxB := WithTenant(context.Background(), tenantB)
	if err := docs.Create(ctxB, &Doc{Title: "b-only"}); err != nil {
		t.Fatal(err)
	}

	// Reads are confined by the explicit tenant scope.
	a, err := docs.Scopes(OwnedByTenant(tenantA)).Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 {
		t.Errorf("tenant A sees %d docs, want 2", len(a))
	}
	for _, d := range a {
		if d.TenantID != tenantA {
			t.Errorf("tenant A read leaked tenant %d", d.TenantID)
		}
	}
	if n, _ := docs.Scopes(OwnedByTenant(tenantB)).Count(context.Background()); n != 1 {
		t.Errorf("tenant B count = %d, want 1", n)
	}

	// A cross-tenant read misses: tenant B cannot see tenant A's row.
	aRow := a[0]
	_, err = docs.Scopes(OwnedByTenant(tenantB)).Filter(query.Col[int64]("id").Eq(aRow.ID)).First(context.Background())
	if !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("cross-tenant read should miss, got err=%v", err)
	}

	// An explicit TenantID on the model is respected (not overwritten by context).
	pinned := &Doc{Title: "pinned", TenantID: tenantB}
	if err := docs.Create(ctxA, pinned); err != nil { // ctx says A, model says B
		t.Fatal(err)
	}
	if pinned.TenantID != tenantB {
		t.Errorf("explicit TenantID overwritten: %d", pinned.TenantID)
	}
}
