package gen

import (
	"strings"
	"testing"
)

const sampleSQL = `
-- name: GetUser :one
-- liteorm:result User
-- liteorm:arg id int64
SELECT id, name, email FROM users WHERE id = ?;

-- name: ListActive :many
-- liteorm:result User
SELECT id, name, email FROM users WHERE active = ? ORDER BY name;

-- name: CountUsers :one
-- liteorm:result int64
SELECT count(*) FROM users;

-- name: Deactivate :exec
UPDATE users SET active = 0 WHERE id = ?;

-- name: PurgeStale :execrows
DELETE FROM users WHERE last_seen < ?;

-- name: InsertUser :execlastid
INSERT INTO users (name, email) VALUES (?, ?);
`

func TestParseQueries(t *testing.T) {
	qs, err := ParseQueries(sampleSQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 6 {
		t.Fatalf("parsed %d queries, want 6", len(qs))
	}
	get := qs[0]
	if get.Name != "GetUser" || get.Cmd != CmdOne || get.Result != "User" {
		t.Errorf("GetUser parsed as %+v", get)
	}
	if len(get.Args) != 1 || get.Args[0].Name != "id" || get.Args[0].Type != "int64" {
		t.Errorf("GetUser args = %+v", get.Args)
	}
	if strings.Contains(get.SQL, ";") {
		t.Errorf("trailing semicolon not stripped: %q", get.SQL)
	}
	// ListActive has one ? but no arg directive → auto-named arg1, type any.
	la := qs[1]
	if len(la.Args) != 1 || la.Args[0].Name != "arg1" || la.Args[0].Type != "any" {
		t.Errorf("ListActive auto-arg = %+v", la.Args)
	}
	// CountUsers has no placeholders → no args.
	if len(qs[2].Args) != 0 {
		t.Errorf("CountUsers args = %+v, want none", qs[2].Args)
	}
	// InsertUser has two ?.
	if len(qs[5].Args) != 2 {
		t.Errorf("InsertUser args = %+v, want 2", qs[5].Args)
	}
}

func TestWriteQueries(t *testing.T) {
	qs, err := ParseQueries(sampleSQL)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteQueries(&b, "models", qs); err != nil {
		t.Fatalf("WriteQueries (also gofmt/parse-validates the output): %v", err)
	}
	out := b.String()
	wants := []string{
		"package models",
		`const getUserSQL = `,
		"func GetUser(ctx context.Context, sess liteorm.Session, id int64) (User, error)",
		"return zero, liteorm.ErrNoRows",
		"func ListActive(ctx context.Context, sess liteorm.Session, arg1 any) ([]User, error)",
		"func CountUsers(ctx context.Context, sess liteorm.Session) (int64, error)",
		"func Deactivate(ctx context.Context, sess liteorm.Session, arg1 any) error",
		"func PurgeStale(ctx context.Context, sess liteorm.Session, arg1 any) (int64, error)",
		"res.RowsAffected()",
		"func InsertUser(ctx context.Context, sess liteorm.Session, arg1 any, arg2 any) (int64, error)",
		"liteorm.LastInsertIder",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("generated source missing %q\n--- got ---\n%s", w, out)
		}
	}
}

func TestParseQueriesErrors(t *testing.T) {
	if _, err := ParseQueries("-- name: Bad :one\nSELECT 1;"); err == nil {
		t.Error(":one without a result directive should error")
	}
	if _, err := ParseQueries("-- name: Bad :frobnicate\nSELECT 1;"); err == nil {
		t.Error("unknown command should error")
	}
}

func TestPlaceholderCountDollar(t *testing.T) {
	// Postgres-style: highest $N wins, even out of order / repeated.
	if n := placeholderCount("SELECT $2, $1, $1 FROM t"); n != 2 {
		t.Errorf("dollar placeholder count = %d, want 2", n)
	}
}
