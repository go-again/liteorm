package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/query"
)

func TestUpsertOnConflict(t *testing.T) {
	ctx := context.Background()
	repo := query.NewRepo[Language](DB)

	l := Language{Code: "up-en", Name: "English"}
	if err := repo.Insert(ctx, &l); err != nil {
		t.Fatal(err)
	}

	// Re-inserting the same unique code upserts: update the name in place.
	again := Language{Code: "up-en", Name: "English (US)"}
	if err := repo.Upsert(ctx, &again, query.OnConflict("code").DoUpdate("name")); err != nil {
		t.Fatal(err)
	}

	got, err := query.Select[Language](DB).Filter(query.Col[string]("code").Eq("up-en")).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("upsert created a duplicate: %d rows for code up-en", len(got))
	}
	if got[0].Name != "English (US)" {
		t.Errorf("upsert name = %q, want English (US)", got[0].Name)
	}
}
