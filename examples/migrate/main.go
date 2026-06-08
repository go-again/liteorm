// Command migrate is an end-to-end tour of liteorm's standalone migration runner
// (liteorm.org/migrate): load versioned SQL migrations from an embedded FS, apply
// them, inspect status, and roll one back — the migrate-CLI shape, on a throwaway
// SQLite database. With no flags it runs the whole demo; pass -cmd to drive one
// step (up / down / status), the skeleton of a real migration command.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/migrate"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	cmd := flag.String("cmd", "demo", "up | down | status | demo")
	flag.Parse()
	if err := run(*cmd); err != nil {
		log.Fatal(err)
	}
}

func run(cmd string) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-migrate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	// Load the embedded migrations (golang-migrate split format is auto-detected).
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	migs, err := migrate.Load(sub)
	if err != nil {
		return err
	}
	m := migrate.New(db)

	switch cmd {
	case "up":
		n, err := m.Up(ctx, migs)
		if err != nil {
			return err
		}
		fmt.Printf("applied %d migration(s)\n", n)
	case "down":
		if err := m.Down(ctx, migs); err != nil {
			return err
		}
		fmt.Println("rolled back one migration")
	case "status":
		return printStatus(ctx, m, migs)
	case "demo":
		return demo(ctx, db, m, migs)
	default:
		return fmt.Errorf("unknown -cmd %q (want up | down | status | demo)", cmd)
	}
	return nil
}

func demo(ctx context.Context, db *liteorm.DB, m *migrate.Migrator, migs []migrate.Migration) error {
	section("Up: apply every pending migration")
	n, err := m.Up(ctx, migs)
	if err != nil {
		return err
	}
	fmt.Printf("applied %d migration(s)\n", n)
	if err := printStatus(ctx, m, migs); err != nil {
		return err
	}

	// The schema the migrations built is now usable.
	if _, err := db.ExecContext(ctx, `INSERT INTO widgets (name, color) VALUES ('cog', 'red')`); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT name, color FROM widgets`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, color string
		if err := rows.Scan(&name, &color); err != nil {
			return err
		}
		fmt.Printf("row: name=%q color=%q (the color column came from migration 2)\n", name, color)
	}

	section("Down: roll back the most recent migration")
	if err := m.Down(ctx, migs); err != nil {
		return err
	}
	if err := printStatus(ctx, m, migs); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func printStatus(ctx context.Context, m *migrate.Migrator, migs []migrate.Migration) error {
	ver, dirty, err := m.Version(ctx)
	if err != nil {
		return err
	}
	statuses, err := m.Status(ctx, migs)
	if err != nil {
		return err
	}
	fmt.Printf("version=%d dirty=%v\n", ver, dirty)
	for _, s := range statuses {
		mark := "pending"
		if s.Applied {
			mark = "applied"
		}
		fmt.Printf("  %06d %-20s %s\n", s.Version, s.Name, mark)
	}
	return nil
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }
