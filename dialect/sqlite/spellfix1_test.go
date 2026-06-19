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
