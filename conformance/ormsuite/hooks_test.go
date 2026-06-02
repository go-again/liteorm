package ormsuite

import (
	"context"
	"errors"
	"testing"

	"liteorm.org/orm"
)

// Hooked exercises lifecycle hooks; it is migrated on demand by the hooks test.
type Hooked struct {
	ID   int64
	Name string
	Slug string
}

func (Hooked) TableName() string { return "hooked" }

// BeforeCreate derives a slug; returning an error from a hook aborts the write.
func (h *Hooked) BeforeCreate(_ context.Context, op *orm.Op[Hooked]) error {
	if op.Model.Name == "boom" {
		return errors.New("hook abort")
	}
	if op.Model.Slug == "" {
		op.Model.Slug = "slug-" + op.Model.Name
	}
	return nil
}

var _ orm.BeforeCreateHook[Hooked] = (*Hooked)(nil)

func TestHooks(t *testing.T) {
	ctx := context.Background()
	if err := orm.AutoMigrate[Hooked](ctx, DB); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[Hooked](DB)

	h := &Hooked{Name: "widget"}
	if err := repo.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if h.Slug != "slug-widget" {
		t.Errorf("BeforeCreate did not set slug in memory: %q", h.Slug)
	}
	got, _ := repo.Get(ctx, h.ID)
	if got.Slug != "slug-widget" {
		t.Errorf("hook result not persisted: %q", got.Slug)
	}

	// A hook error aborts the create — no row is written.
	if err := repo.Create(ctx, &Hooked{Name: "boom"}); err == nil {
		t.Error("a hook error should abort Create")
	}
	if n, _ := repo.Where("name = ?", "boom").Count(ctx); n != 0 {
		t.Errorf("aborted create wrote %d rows, want 0", n)
	}
}
