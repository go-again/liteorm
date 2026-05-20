// Command sqlc-gen-liteorm is a sqlc codegen plugin: sqlc parses the schema and
// queries, then invokes this binary as a process plugin, writing a serialized
// GenerateRequest to stdin and reading a GenerateResponse from stdout. The
// plugin emits liteorm-runtime typed query functions — so a team already using
// sqlc gets liteorm's runtime (normalized errors, capability fast paths, the
// dual front-ends) without rewriting their query files.
//
// Wire it up in sqlc.yaml:
//
//	version: "2"
//	plugins:
//	  - name: liteorm
//	    process:
//	      cmd: sqlc-gen-liteorm
//	sql:
//	  - schema: schema.sql
//	    queries: query.sql
//	    engine: postgresql
//	    codegen:
//	      - plugin: liteorm
//	        out: db
//	        options: { package: db }
package main

import (
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/proto"

	"liteorm.org/cmd/sqlc-gen-liteorm/plugin"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sqlc-gen-liteorm:", err)
		os.Exit(1)
	}
}

func run() error {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var req plugin.GenerateRequest
	if err := proto.Unmarshal(in, &req); err != nil {
		return fmt.Errorf("unmarshal GenerateRequest: %w", err)
	}
	resp, err := generate(&req)
	if err != nil {
		return err
	}
	out, err := proto.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal GenerateResponse: %w", err)
	}
	_, err = os.Stdout.Write(out)
	return err
}
