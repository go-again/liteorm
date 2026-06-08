// Command codegen demonstrates `liteorm gen`: emit typed Column[V] constants from
// a Go model, which gives the query front-end compile-time COLUMN safety:
//
//	query.Select[User](db).Filter(UserColumns.Name.Eq("alice"))
//
// In real use this runs under `//go:generate` and writes to a file; here it
// prints the generated source to stdout.
package main

import (
	"os"
	"time"

	"liteorm.org/gen"
)

type User struct {
	ID        int64
	Name      string
	Email     string
	CreatedAt time.Time
}

func (User) TableName() string { return "users" }

func main() {
	if err := gen.WriteColumns(os.Stdout, "models", gen.FromType[User]()); err != nil {
		panic(err)
	}
}
