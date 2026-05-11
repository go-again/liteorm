package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePair(t *testing.T) {
	dir := t.TempDir()

	up, down, err := WritePair(dir, 7, "Add Bio Column!",
		"ALTER TABLE users ADD COLUMN bio TEXT;",
		"ALTER TABLE users DROP COLUMN bio;")
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(up); base != "000007_add_bio_column.up.sql" {
		t.Errorf("up filename = %q", base)
	}
	if base := filepath.Base(down); base != "000007_add_bio_column.down.sql" {
		t.Errorf("down filename = %q", base)
	}

	// The pair must load back through the runner's own loader.
	migs, err := Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 1 {
		t.Fatalf("loaded %d migrations, want 1", len(migs))
	}
	m := migs[0]
	if m.Version != 7 || m.Name != "add_bio_column" {
		t.Errorf("loaded version/name = %d/%q", m.Version, m.Name)
	}
	if !strings.Contains(m.Up, "ADD COLUMN bio") || !strings.Contains(m.Down, "DROP COLUMN bio") {
		t.Errorf("loaded up/down mismatch:\nup=%q\ndown=%q", m.Up, m.Down)
	}
}

func TestWritePairEmptyUpErrors(t *testing.T) {
	if _, _, err := WritePair(t.TempDir(), 1, "x", "   ", "down"); err == nil {
		t.Fatal("empty up should error")
	}
}

func TestWritePairIrreversible(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := WritePair(dir, 3, "seed data", "INSERT INTO t VALUES (1);", ""); err != nil {
		t.Fatal(err)
	}
	migs, err := Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 1 || !strings.Contains(migs[0].Down, "irreversible") {
		t.Fatalf("empty down should become an irreversible-comment file, got %q", migs[0].Down)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add Bio Column":     "add_bio_column",
		"  spaces  ":         "spaces",
		"weird!!!chars###":   "weird_chars",
		"":                   "migration",
		"already_snake_case": "already_snake_case",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
