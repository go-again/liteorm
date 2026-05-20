package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"liteorm.org/cmd/sqlc-gen-liteorm/plugin"
)

// TestGeneratedCompiles writes the plugin's output into a throwaway module that
// requires the real liteorm root (driver-free, so it resolves offline) and runs
// `go build` — proving the emitted code compiles against liteorm's actual
// runtime API, not just that it parses.
func TestGeneratedCompiles(t *testing.T) {
	req := &plugin.GenerateRequest{
		PluginOptions: []byte(`{"package":"db"}`),
		Queries: []*plugin.Query{
			{Name: "GetA", Cmd: ":one", Text: "SELECT id, name FROM a WHERE id = $1",
				Columns: []*plugin.Column{col("id", "bigint", true), col("name", "text", false)},
				Params:  []*plugin.Parameter{{Number: 1, Column: col("id", "bigint", true)}}},
			{Name: "ListA", Cmd: ":many", Text: "SELECT id, name FROM a",
				Columns: []*plugin.Column{col("id", "bigint", true), col("name", "text", false)}},
			{Name: "CountA", Cmd: ":one", Text: "SELECT count(*) FROM a",
				Columns: []*plugin.Column{col("n", "bigint", true)}},
			{Name: "DelA", Cmd: ":exec", Text: "DELETE FROM a WHERE id = $1",
				Params: []*plugin.Parameter{{Number: 1, Column: col("id", "bigint", true)}}},
			{Name: "InsA", Cmd: ":execlastid", Text: "INSERT INTO a (name) VALUES (?)",
				Params: []*plugin.Parameter{{Number: 1, Column: col("name", "text", true)}}},
		},
	}
	resp, err := generate(req)
	if err != nil {
		t.Fatal(err)
	}

	_, thisFile, _, _ := runtime.Caller(0)
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "query.liteorm.go"), resp.GetFiles()[0].GetContents(), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module sqlcgen_compile_test\n\ngo 1.25.7\n\nrequire liteorm.org v0.0.0\n\nreplace liteorm.org => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed to compile against liteorm runtime: %v\n%s", err, out)
	}
}
