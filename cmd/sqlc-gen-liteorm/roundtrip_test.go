package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"liteorm.org/cmd/sqlc-gen-liteorm/plugin"
)

// TestProcessPluginRoundtrip exercises the exact contract sqlc uses: build the
// plugin binary, write a serialized GenerateRequest to its stdin, and decode the
// GenerateResponse from its stdout.
func TestProcessPluginRoundtrip(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sqlc-gen-liteorm")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}

	req := &plugin.GenerateRequest{
		Queries: []*plugin.Query{
			{Name: "Ping", Cmd: ":one", Text: "SELECT 1", Columns: []*plugin.Column{col("one", "integer", true)}},
		},
	}
	in, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run plugin: %v\nstderr: %s", err, stderr.String())
	}

	var resp plugin.GenerateResponse
	if err := proto.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.GetFiles()) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.GetFiles()))
	}
	if !bytes.Contains(resp.GetFiles()[0].GetContents(), []byte("func Ping(")) {
		t.Errorf("response missing the generated Ping func:\n%s", resp.GetFiles()[0].GetContents())
	}
}
