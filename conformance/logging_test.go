package conformance_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	liteormlog "liteorm.org/log"
	"liteorm.org/query"
)

type logRow struct {
	ID   int64
	Name string
}

func (logRow) TableName() string { return "logrows" }

// TestStatementLogging proves the dev-logging path end to end: the executed SQL,
// its args, and the Go source location are captured, and disabling debug logging
// is silent.
func TestStatementLogging(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	// Colorless dev handler at debug, capturing into buf.
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "log.db"),
		liteorm.WithLogger(liteormlog.New(&buf, &liteormlog.Options{Color: false})))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `CREATE TABLE logrows (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := query.NewRepo[logRow](db).Insert(ctx, &logRow{Name: "ada"}); err != nil { // <- the issuing line
		t.Fatal(err)
	}
	_, _ = query.Select[logRow](db).Filter(query.Col[string]("name").Eq("ada")).All(ctx)

	out := buf.String()
	if !strings.Contains(out, "INSERT INTO") || !strings.Contains(out, "SELECT") {
		t.Fatalf("log missing executed statements:\n%s", out)
	}
	if !strings.Contains(out, `args=["ada"]`) { // strings are quoted so spaces are unambiguous
		t.Errorf("log missing bound args:\n%s", out)
	}
	// The caller location must point back into THIS test file, not into liteorm.
	if !strings.Contains(out, "logging_test.go:") {
		t.Errorf("log missing caller pointing at the issuing Go line:\n%s", out)
	}

	// With the logger above debug, nothing is logged.
	buf.Reset()
	silent, _ := sqlite.Open(filepath.Join(t.TempDir(), "silent.db"),
		liteorm.WithLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))))
	defer silent.Close()
	_, _ = silent.ExecContext(ctx, "SELECT 1")
	if buf.Len() != 0 {
		t.Errorf("statements must not be logged above debug level, got:\n%s", buf.String())
	}
}
