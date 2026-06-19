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

func TestUpsertDoNothing(t *testing.T) {
	ctx := context.Background()
	repo := query.NewRepo[Language](DB)

	if err := repo.Insert(ctx, &Language{Code: "dn-fr", Name: "French"}); err != nil {
		t.Fatal(err)
	}

	// Re-inserting the same unique code with DoNothing ignores the conflict: the
	// original row is untouched and no duplicate is created — across every dialect
	// (DO NOTHING / no-op ON DUPLICATE KEY / MERGE without WHEN MATCHED).
	dup := Language{Code: "dn-fr", Name: "Français"}
	if err := repo.Upsert(ctx, &dup, query.OnConflict("code").DoNothing()); err != nil {
		t.Fatal(err)
	}

	got, err := query.Select[Language](DB).Filter(query.Col[string]("code").Eq("dn-fr")).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("DoNothing created a duplicate: %d rows for code dn-fr", len(got))
	}
	if got[0].Name != "French" {
		t.Errorf("DoNothing name = %q, want French (the conflict must be ignored, not updated)", got[0].Name)
	}
}
