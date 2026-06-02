package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

// TestFilteredEagerLoad covers LoadWhere/LoadOrderBy: the batched children query
// is filtered and ordered, still in one query (N+1-safe).
func TestFilteredEagerLoad(t *testing.T) {
	ctx := context.Background()
	u := &User{Name: "fl-owner"}
	mustCreate(t, u)
	for _, n := range []string{"fl-a", "fl-b", "fl-c"} {
		mustCreate(t, &Pet{UserID: u.ID, Name: n})
	}

	// Ordered descending.
	ordered := []User{*u}
	if err := orm.Load[User, Pet](ctx, DB, ordered, "Pets", orm.LoadOrderBy("name DESC")); err != nil {
		t.Fatal(err)
	}
	got := ordered[0].Pets
	if len(got) != 3 || got[0].Name != "fl-c" || got[2].Name != "fl-a" {
		t.Errorf("ordered load = %v, want [fl-c fl-b fl-a]", petNames(got))
	}

	// Filtered to a single child.
	filtered := []User{*u}
	if err := orm.Load[User, Pet](ctx, DB, filtered, "Pets", orm.LoadWhere("name = ?", "fl-b")); err != nil {
		t.Fatal(err)
	}
	if len(filtered[0].Pets) != 1 || filtered[0].Pets[0].Name != "fl-b" {
		t.Errorf("filtered load = %v, want [fl-b]", petNames(filtered[0].Pets))
	}

	// Filtering a many-to-many load is an explicit error (documented follow-on).
	if err := orm.Load[User, Language](ctx, DB, []User{*u}, "Languages", orm.LoadWhere("1=1")); err == nil {
		t.Error("filtered/ordered eager load on a many-to-many relation should error")
	}
}

func petNames(ps []Pet) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
