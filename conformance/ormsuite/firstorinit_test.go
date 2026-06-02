package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

// TestFirstOrInit covers the non-persisting load-or-prepare verb: a match is
// loaded; a miss leaves the caller's defaults in place and writes nothing.
func TestFirstOrInit(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	seed := &User{Name: "foi-existing", Age: 7}
	mustCreate(t, seed)

	// Found: the row is loaded into v.
	got := &User{Name: "foi-existing"}
	found, err := repo.FirstOrInit(ctx, got, query.Col[string]("name").Eq("foi-existing"))
	if err != nil || !found {
		t.Fatalf("FirstOrInit(existing): found=%v err=%v", found, err)
	}
	if got.ID != seed.ID || got.Age != 7 {
		t.Errorf("FirstOrInit did not load the row: %+v", got)
	}

	// Not found: v keeps its prepared defaults, and NOTHING is persisted.
	before, _ := repo.Where("name = ?", "foi-missing").Count(ctx)
	prep := &User{Name: "foi-missing", Age: 99}
	found, err = repo.FirstOrInit(ctx, prep, query.Col[string]("name").Eq("foi-missing"))
	if err != nil || found {
		t.Fatalf("FirstOrInit(missing): found=%v err=%v", found, err)
	}
	if prep.ID != 0 || prep.Age != 99 {
		t.Errorf("FirstOrInit(missing) should leave defaults untouched: %+v", prep)
	}
	after, _ := repo.Where("name = ?", "foi-missing").Count(ctx)
	if before != 0 || after != 0 {
		t.Errorf("FirstOrInit must not persist: count before=%d after=%d", before, after)
	}
}
