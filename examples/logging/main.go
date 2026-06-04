// Command logging demonstrates liteorm's statement logging: every executed SQL
// statement is logged with its timing, bind arguments, rows affected, and the Go
// source line that issued it — so during development you can watch the queries
// and trace each one back to your code. liteorm logs at slog debug level through
// whatever *slog.Logger you configure, so you choose the output: the colored
// human-readable handler in liteorm.org/log, or any standard slog handler (JSON,
// text) for structured logs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	devlog "liteorm.org/log"
	"liteorm.org/query"
)

type Widget struct {
	ID    int64
	Name  string
	Price int64
}

func (Widget) TableName() string { return "widgets" }

func main() {
	if err := run(); err != nil {
		// errors are logged too; this is the program-level fallback
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	dir, _ := os.MkdirTemp("", "liteorm-logging-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "app.db")

	// ---- 1. Colored, human-readable dev logging ----
	// Color is on by default for a real terminal; this example forces it off so
	// the captured output stays clean. (Set NO_COLOR=1 to disable globally.)
	fmt.Println("── colored/plain dev handler (liteorm.org/log) ──")
	db, err := sqlite.Open(path, liteorm.WithLogger(
		devlog.New(os.Stdout, &devlog.Options{Color: false})))
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE widgets (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, price INTEGER NOT NULL)`); err != nil {
		return err
	}
	repo := query.NewRepo[Widget](db)
	_ = repo.Insert(ctx, &Widget{Name: "Gear", Price: 500}) // each statement below is logged with this file:line
	_ = repo.Insert(ctx, &Widget{Name: "Cog", Price: 250})
	_, _ = query.Select[Widget](db).Filter(query.Col[int64]("price").Gt(300)).All(ctx)
	_ = repo.Delete(ctx, 2)
	db.Close()

	// ---- 2. Structured slog (JSON) — same events, machine-readable ----
	fmt.Println("\n── structured slog JSON handler ──")
	jsonLog := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	db2, err := sqlite.Open(filepath.Join(dir, "app2.db"), liteorm.WithLogger(jsonLog))
	if err != nil {
		return err
	}
	defer db2.Close()
	if _, err := db2.ExecContext(ctx, `CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price INTEGER NOT NULL)`); err != nil {
		return err
	}
	_, _ = db2.QueryContext(ctx, `SELECT count(*) FROM widgets`)

	// ---- 3. Redacting argument values ----
	fmt.Println("\n── args redacted (WithSQLArgs(false)) ──")
	db3, _ := sqlite.Open(filepath.Join(dir, "app3.db"),
		liteorm.WithLogger(devlog.New(os.Stdout, &devlog.Options{Color: false})),
		liteorm.WithSQLArgs(false))
	defer db3.Close()
	_, _ = db3.ExecContext(ctx, `CREATE TABLE secrets (id INTEGER PRIMARY KEY, token TEXT)`)
	_, _ = db3.ExecContext(ctx, `INSERT INTO secrets (token) VALUES (?)`, "s3cr3t")
	return nil
}
