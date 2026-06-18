package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	_ "gosqlite.org/ext/regexp/auto" // global: registers the RE2 REGEXP operator on every connection
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

type account struct {
	ID    int64
	Email string
}

func (account) TableName() string { return "accounts" }

// gosqlite registers scalar functions globally (via a connect hook), so a
// function like REGEXP flows through liteorm with no liteorm-specific glue — this
// is what makes gosqlite's gorm-specific regexp wiring unnecessary here.
func TestRegexp_WorksInQueryPredicate(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "rx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts (id INTEGER PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"a@example.com", "b@example.org", "c@example.com"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts(email) VALUES (?)`, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := query.Select[account](db).
		Where("email REGEXP ?", `^.*@example\.com$`).
		All(ctx)
	if err != nil {
		t.Fatalf("REGEXP query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("REGEXP matched %d rows, want 2 (the .com addresses)", len(got))
	}
	for _, a := range got {
		if a.Email[len(a.Email)-4:] != ".com" {
			t.Errorf("unexpected match %q", a.Email)
		}
	}
}

func TestWhereRegex_GlobOptimization(t *testing.T) {
	// Anchored pattern -> GLOB prefix + REGEXP residual (index-friendly).
	frag, args := sqlite.WhereRegex("email", `^a@`)
	if !strings.Contains(frag, "GLOB") || !strings.Contains(frag, "REGEXP") {
		t.Errorf("anchored pattern should emit GLOB+REGEXP, got %q", frag)
	}
	if len(args) != 2 {
		t.Errorf("anchored pattern should bind prefix+pattern, got %v", args)
	}
	// Unanchored -> plain REGEXP, single bind.
	frag, args = sqlite.WhereRegex("email", `example`)
	if strings.Contains(frag, "GLOB") || len(args) != 1 {
		t.Errorf("unanchored pattern should be a plain REGEXP, got %q %v", frag, args)
	}

	// End-to-end: the optimized clause selects the same rows as plain REGEXP.
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "rx2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts (id INTEGER PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"a@example.com", "ab@example.com", "b@example.com"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts(email) VALUES (?)`, e); err != nil {
			t.Fatal(err)
		}
	}
	frag, args = sqlite.WhereRegex("email", `^a@`)
	got, err := query.Select[account](db).Where(frag, args...).All(ctx)
	if err != nil {
		t.Fatalf("WhereRegex query: %v", err)
	}
	if len(got) != 1 || got[0].Email != "a@example.com" {
		t.Errorf("WhereRegex `^a@` matched %d rows, want only a@example.com", len(got))
	}
}
