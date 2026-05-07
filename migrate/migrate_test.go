package migrate

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"two", "CREATE TABLE a (x int); CREATE TABLE b (y int);",
			[]string{"CREATE TABLE a (x int)", "CREATE TABLE b (y int)"}},
		{"line comment", "-- hi\nSELECT 1;\nSELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in string", "INSERT INTO t VALUES ('a;b');", []string{"INSERT INTO t VALUES ('a;b')"}},
		{"escaped quote", "INSERT INTO t VALUES ('it''s; ok');", []string{"INSERT INTO t VALUES ('it''s; ok')"}},
		{"block comment", "/* drop; me */ SELECT 1;", []string{"SELECT 1"}},
		{"dollar quote", "CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql;",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql"}},
		{"trailing no semicolon", "SELECT 1", []string{"SELECT 1"}},
		{"empty", "  \n -- nothing\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitStatements(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

func TestLoadFormats(t *testing.T) {
	fsys := fstest.MapFS{
		"001_init.up.sql":   {Data: []byte("CREATE TABLE a (x int);")},
		"001_init.down.sql": {Data: []byte("DROP TABLE a;")},
		"002_goose.sql":     {Data: []byte("-- +goose Up\nCREATE TABLE b (y int);\n-- +goose Down\nDROP TABLE b;")},
		"003_plain.sql":     {Data: []byte("CREATE TABLE c (z int);")},
		"readme.txt":        {Data: []byte("ignored")},
	}
	migs, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 3 {
		t.Fatalf("got %d migrations, want 3", len(migs))
	}
	if migs[0].Version != 1 || migs[1].Version != 2 || migs[2].Version != 3 {
		t.Errorf("versions = %d,%d,%d", migs[0].Version, migs[1].Version, migs[2].Version)
	}
	// split format: up + down
	if migs[0].Up != "CREATE TABLE a (x int);" || migs[0].Down != "DROP TABLE a;" {
		t.Errorf("v1 = up:%q down:%q", migs[0].Up, migs[0].Down)
	}
	// goose-annotated: sections extracted
	if want := "CREATE TABLE b (y int);"; trim(migs[1].Up) != want || trim(migs[1].Down) != "DROP TABLE b;" {
		t.Errorf("v2 goose = up:%q down:%q", migs[1].Up, migs[1].Down)
	}
	// plain: up-only
	if migs[2].Up != "CREATE TABLE c (z int);" || migs[2].Down != "" {
		t.Errorf("v3 plain = up:%q down:%q", migs[2].Up, migs[2].Down)
	}
}

func trim(s string) string {
	// goose sections keep surrounding newlines; normalize for the assertion.
	for len(s) > 0 && (s[0] == '\n' || s[0] == ' ') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
