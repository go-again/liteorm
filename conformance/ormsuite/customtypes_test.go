package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

// TestCustomScalarTypes round-trips a CSV-encoded slice and a JSON-encoded map
// through ordinary columns — the Valuer/Scanner ("conversion") pattern works on
// every backend without a native array/JSON column type.
func TestCustomScalarTypes(t *testing.T) {
	ctx := context.Background()
	events := orm.NewRepo[Event](DB)

	e := &Event{
		Name:   "launch",
		Labels: CSV{"go", "db", "orm"},
		Props:  JSONMap{"env": "prod", "tier": "gold"},
	}
	if err := events.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := events.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Labels) != 3 || got.Labels[0] != "go" || got.Labels[2] != "orm" {
		t.Errorf("CSV did not round-trip: %#v", got.Labels)
	}
	if got.Props["env"] != "prod" || got.Props["tier"] != "gold" {
		t.Errorf("JSON map did not round-trip: %#v", got.Props)
	}

	// An empty/nil custom value round-trips to an empty (not garbage) value.
	blank := &Event{Name: "blank"}
	if err := events.Create(ctx, blank); err != nil {
		t.Fatal(err)
	}
	reread, err := events.Get(ctx, blank.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reread.Labels) != 0 || len(reread.Props) != 0 {
		t.Errorf("blank custom values came back non-empty: labels=%#v props=%#v", reread.Labels, reread.Props)
	}
}
