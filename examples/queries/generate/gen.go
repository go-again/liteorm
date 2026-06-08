// Command generate turns queries.sql into queries.gen.go using liteorm's
// SQL→typed-Go codegen. It is invoked by `go generate` from the parent example
// (see the directive in main.go) and runs with the example directory as its
// working directory.
package main

import (
	"log"
	"os"

	"liteorm.org/gen"
)

func main() {
	src, err := os.ReadFile("queries.sql")
	if err != nil {
		log.Fatal(err)
	}
	queries, err := gen.ParseQueries(string(src))
	if err != nil {
		log.Fatal(err)
	}
	out, err := os.Create("queries.gen.go")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if err := gen.WriteQueries(out, "main", queries); err != nil {
		log.Fatal(err)
	}
}
