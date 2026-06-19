package sqlite_test

import (
	"context"
	"testing"

	_ "gosqlite.org/ext/spellfix1/auto" // registers the spellfix1 vtab module pool-wide
	"liteorm.org/query"
)

// Verifies the documented "fuzzy correction with spellfix1" recipe end to end:
// create + populate the vtab with ExecContext, then a typed query.Match query
// ordered by the edit distance spellfix1 computes.
type spellVocab struct {
	Word     string
	Distance int
}

func (spellVocab) TableName() string { return "vocab" }

func TestSpellfix1_MatchRecipe(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE vocab USING spellfix1"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"kennedy", "jefferson", "lincoln"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO vocab(word) VALUES (?)", w); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := query.Select[spellVocab](db).
		Filter(query.Match("word", "kenedy")). // a misspelling
		OrderBy("distance ASC").
		All(ctx)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(hits) == 0 || hits[0].Word != "kennedy" {
		t.Fatalf("fuzzy correct 'kenedy' = %+v, want kennedy first", hits)
	}
}

// spellfix1's "scope" is a HIDDEN constraint column — present on the vtab, not on
// the model. query.Col[int]("scope").Unvalidated() lets the WHERE constraint be
// typed and dialect-quoted instead of a raw Where fragment alongside Match.
func TestSpellfix1_HiddenScopeColumn(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE vocab USING spellfix1"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"kennedy", "jefferson", "lincoln"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO vocab(word) VALUES (?)", w); err != nil {
			t.Fatal(err)
		}
	}
	hit, err := query.Select[spellVocab](db).
		Filter(query.And(
			query.Match("word", "kenedy"),
			query.Col[int]("scope").Unvalidated().Eq(4), // HIDDEN: max search scope
		)).
		OrderBy("distance ASC").
		First(ctx)
	if err != nil {
		t.Fatalf("scoped fuzzy correct: %v", err)
	}
	if hit.Word != "kennedy" {
		t.Fatalf("scoped correct 'kenedy' = %q, want kennedy", hit.Word)
	}
}
