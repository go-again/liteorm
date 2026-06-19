package search_test

import (
	"context"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/search"
)

func TestSpellfix_CreateAddCorrect(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "sf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sf, err := search.NewSpellfix(ctx, db, "vocab")
	if err != nil {
		t.Fatalf("NewSpellfix: %v", err)
	}
	if err := sf.Add(ctx, "kennedy", "jefferson", "lincoln"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if n, err := sf.Size(ctx); err != nil || n != 3 {
		t.Fatalf("Size = %d, err %v; want 3", n, err)
	}
	// The vocabulary is a set: re-adding a word is a no-op.
	if err := sf.Add(ctx, "kennedy"); err != nil {
		t.Fatal(err)
	}
	if n, _ := sf.Size(ctx); n != 3 {
		t.Errorf("Size after duplicate = %d, want 3", n)
	}

	hits, err := sf.Correct(ctx, "kenedy", search.WithLimit(1)) // a misspelling
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	if len(hits) != 1 || hits[0].Word != "kennedy" {
		t.Fatalf("Correct('kenedy') = %+v, want kennedy nearest", hits)
	}
	if hits[0].Distance <= 0 {
		t.Errorf("expected a positive edit distance, got %d", hits[0].Distance)
	}

	// NewSpellfix is idempotent; OpenSpellfix attaches to the existing vocab.
	if _, err := search.NewSpellfix(ctx, db, "vocab"); err != nil {
		t.Errorf("NewSpellfix idempotent: %v", err)
	}
	reopened, err := search.OpenSpellfix(db, "vocab")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := reopened.Size(ctx); n != 3 {
		t.Errorf("reopened Size = %d, want 3", n)
	}
}
