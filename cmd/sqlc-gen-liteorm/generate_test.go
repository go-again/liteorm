package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"liteorm.org/cmd/sqlc-gen-liteorm/plugin"
)

func col(name, typ string, notNull bool) *plugin.Column {
	return &plugin.Column{Name: name, NotNull: notNull, Type: &plugin.Identifier{Name: typ}}
}

func TestGenerate(t *testing.T) {
	req := &plugin.GenerateRequest{
		PluginOptions: []byte(`{"package":"db"}`),
		Queries: []*plugin.Query{
			{
				Name: "GetAuthor", Cmd: ":one",
				Text:    "SELECT id, name, bio FROM authors WHERE id = $1",
				Columns: []*plugin.Column{col("id", "bigint", true), col("name", "text", true), col("bio", "text", false)},
				Params:  []*plugin.Parameter{{Number: 1, Column: col("id", "bigint", true)}},
			},
			{
				Name: "ListAuthors", Cmd: ":many",
				Text:    "SELECT id, name, bio FROM authors ORDER BY name",
				Columns: []*plugin.Column{col("id", "bigint", true), col("name", "text", true), col("bio", "text", false)},
			},
			{
				Name: "CountAuthors", Cmd: ":one",
				Text:    "SELECT count(*) FROM authors",
				Columns: []*plugin.Column{col("count", "bigint", true)},
			},
			{
				Name: "DeleteAuthor", Cmd: ":exec",
				Text:   "DELETE FROM authors WHERE id = $1",
				Params: []*plugin.Parameter{{Number: 1, Column: col("id", "bigint", true)}},
			},
			{
				Name: "CreateAuthor", Cmd: ":execlastid",
				Text:   "INSERT INTO authors (name, bio) VALUES (?, ?)",
				Params: []*plugin.Parameter{{Number: 1, Column: col("name", "text", true)}, {Number: 2, Column: col("bio", "text", false)}},
			},
		},
	}

	resp, err := generate(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetFiles()) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.GetFiles()))
	}
	f := resp.GetFiles()[0]
	if f.GetName() != "query.liteorm.go" {
		t.Errorf("file name = %q", f.GetName())
	}
	src := string(f.GetContents())

	// Output must be valid Go.
	if _, perr := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments); perr != nil {
		t.Fatalf("generated source is not valid Go: %v\n%s", perr, src)
	}

	wants := []string{
		"package db",
		`liteorm "liteorm.org"`,
		`"liteorm.org/query"`,
		"type GetAuthorRow struct {",
		"`db:\"id\"`",
		"`db:\"name\"`",
		"*string `db:\"bio\"`", // nullable → pointer
		"func GetAuthor(ctx context.Context, sess liteorm.Session, id int64) (GetAuthorRow, error)",
		"func ListAuthors(ctx context.Context, sess liteorm.Session) ([]ListAuthorsRow, error)",
		"func CountAuthors(ctx context.Context, sess liteorm.Session) (int64, error)", // scalar
		"func DeleteAuthor(ctx context.Context, sess liteorm.Session, id int64) error",
		"func CreateAuthor(ctx context.Context, sess liteorm.Session, name string, bio *string) (int64, error)",
		"liteorm.LastInsertIder",
	}
	for _, w := range wants {
		if !strings.Contains(src, w) {
			t.Errorf("generated source missing %q\n--- got ---\n%s", w, src)
		}
	}
}

func TestGoTypeMapping(t *testing.T) {
	cases := []struct {
		typ     string
		notNull bool
		array   bool
		want    string
	}{
		{"bigint", true, false, "int64"},
		{"text", true, false, "string"},
		{"text", false, false, "*string"},
		{"boolean", true, false, "bool"},
		{"timestamptz", true, false, "time.Time"},
		{"numeric", true, false, "string"},
		{"text", true, true, "[]string"},
		{"bytea", false, false, "[]byte"},
		{"weirdtype", true, false, "any"},
	}
	for _, c := range cases {
		got, _ := goType(&plugin.Column{Type: &plugin.Identifier{Name: c.typ}, NotNull: c.notNull, IsArray: c.array}, false)
		if got != c.want {
			t.Errorf("goType(%s notNull=%v array=%v) = %q, want %q", c.typ, c.notNull, c.array, got, c.want)
		}
	}
}
