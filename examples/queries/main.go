// Command queries demonstrates liteorm's SQL→typed-Go codegen end to end: the
// annotated queries.sql is compiled by `go generate` into queries.gen.go (typed
// functions over the liteorm runtime), and this program calls those generated
// functions against a live SQLite database. The generated file is checked in, so
// `just examples` builds and runs it without a generation step.
package main

//go:generate go run ./generate

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"liteorm.org/dialect/sqlite"
)

// User is the result type the generated :one/:many queries scan into. The
// generated code references it by name, so it must live in this package.
type User struct {
	ID       int64
	Name     string
	Email    string
	Active   bool
	LastSeen string
}

func (User) TableName() string { return "users" }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-queries-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE users (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		name      TEXT NOT NULL,
		email     TEXT NOT NULL,
		active    INTEGER NOT NULL,
		last_seen TEXT NOT NULL
	)`); err != nil {
		return err
	}

	// All of the following are generated functions from queries.sql.
	seed := []struct {
		name, email, seen string
		active            int
	}{
		{"Ada", "ada@x.io", "2024-06-01", 1},
		{"Grace", "grace@x.io", "2024-06-10", 1},
		{"Stale Sam", "sam@x.io", "2019-01-01", 1},
	}
	var adaID int64
	for _, s := range seed {
		id, err := InsertUser(ctx, db, s.name, s.email, s.active, s.seen)
		if err != nil {
			return err
		}
		if s.name == "Ada" {
			adaID = id
		}
	}

	total, err := CountUsers(ctx, db)
	if err != nil {
		return err
	}
	fmt.Printf("CountUsers (:one int64) → %d\n", total)

	ada, err := GetUser(ctx, db, adaID)
	if err != nil {
		return err
	}
	fmt.Printf("GetUser (:one User) → %s <%s> active=%v\n", ada.Name, ada.Email, ada.Active)

	purged, err := PurgeStale(ctx, db, "2020-01-01")
	if err != nil {
		return err
	}
	fmt.Printf("PurgeStale (:execrows) → removed %d stale user(s)\n", purged)

	if err := Deactivate(ctx, db, adaID); err != nil {
		return err
	}
	fmt.Println("Deactivate (:exec) → Ada deactivated")

	active, err := ListActive(ctx, db, true)
	if err != nil {
		return err
	}
	fmt.Printf("ListActive (:many []User) → %d still active: ", len(active))
	for i, u := range active {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(u.Name)
	}
	fmt.Println()
	return nil
}
