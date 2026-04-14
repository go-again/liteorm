package log

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	liteorm "liteorm.org"
)

func record(msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC), slog.LevelDebug, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

func TestStatementLineFormat(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, &Options{Color: false, AbsPath: true})
	_ = h.Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT *\n  FROM users\n  WHERE id = $1"),
		slog.Any(liteorm.AttrArgs, []any{42}),
		slog.Duration(liteorm.AttrDur, 1500*time.Microsecond),
		slog.String(liteorm.AttrCaller, "/app/main.go:31"),
	))
	out := buf.String()
	for _, want := range []string{
		"[liteorm]",
		"SELECT * FROM users WHERE id = $1", // multi-line collapsed to one line
		"args=[42]",
		"1.50ms",
		"(/app/main.go:31)", // AbsPath keeps the path absolute
	} {
		if !strings.Contains(out, want) {
			t.Errorf("line %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "\033[") {
		t.Errorf("Color:false must produce no ANSI codes: %q", out)
	}
}

func TestRelativeCallerPath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(cwd, "sub", "main.go") + ":42"

	// Default: the caller path is shown relative to the working directory.
	var buf bytes.Buffer
	_ = NewHandler(&buf, &Options{Color: false}).Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT 1"),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
		slog.String(liteorm.AttrCaller, abs),
	))
	if want := "(" + filepath.Join("sub", "main.go") + ":42)"; !strings.Contains(buf.String(), want) {
		t.Errorf("default should relativize caller: %q missing %q", buf.String(), want)
	}

	// AbsPath: the absolute caller path is preserved.
	buf.Reset()
	_ = NewHandler(&buf, &Options{Color: false, AbsPath: true}).Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT 1"),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
		slog.String(liteorm.AttrCaller, abs),
	))
	if !strings.Contains(buf.String(), "("+abs+")") {
		t.Errorf("AbsPath should keep caller absolute: %q missing %q", buf.String(), abs)
	}

	// A caller outside the working directory keeps its absolute path (a long
	// "../../.." relative path would be uglier, not shorter).
	buf.Reset()
	outside := filepath.Join(filepath.VolumeName(cwd), string(filepath.Separator), "elsewhere", "pkg", "x.go") + ":7"
	_ = NewHandler(&buf, &Options{Color: false}).Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT 1"),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
		slog.String(liteorm.AttrCaller, outside),
	))
	if !strings.Contains(buf.String(), "("+outside+")") {
		t.Errorf("out-of-cwd caller should stay absolute: %q missing %q", buf.String(), outside)
	}

	// A caller value with no numeric line suffix is left untouched (no bad split).
	buf.Reset()
	_ = NewHandler(&buf, &Options{Color: false}).Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT 1"),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
		slog.String(liteorm.AttrCaller, "/app/main.go"),
	))
	if !strings.Contains(buf.String(), "(/app/main.go)") {
		t.Errorf("caller without a line suffix should be unchanged: %q", buf.String())
	}
}

func TestArgsAreDelimited(t *testing.T) {
	// Each bind value is delimited and strings are quoted, so a value with spaces
	// is unambiguous; a nil binds as NULL.
	var buf bytes.Buffer
	_ = NewHandler(&buf, &Options{Color: false}).Handle(context.Background(), record(liteorm.MsgExec,
		slog.String(liteorm.AttrSQL, "INSERT INTO t VALUES (?, ?, ?)"),
		slog.Any(liteorm.AttrArgs, []any{42, "ada lovelace", nil}),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
	))
	if want := `args=[42, "ada lovelace", NULL]`; !strings.Contains(buf.String(), want) {
		t.Errorf("args not delimited: %q missing %q", buf.String(), want)
	}

	// Redacted args (logged as a count) render as a hidden marker.
	buf.Reset()
	_ = NewHandler(&buf, &Options{Color: false}).Handle(context.Background(), record(liteorm.MsgExec,
		slog.String(liteorm.AttrSQL, "INSERT INTO secrets VALUES (?)"),
		slog.Int(liteorm.AttrArgs, 1),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
	))
	if !strings.Contains(buf.String(), "args=<1 hidden>") {
		t.Errorf("redacted args should show a hidden count: %q", buf.String())
	}
}

func TestExecLineShowsRowsAndError(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, &Options{Color: false})
	_ = h.Handle(context.Background(), record(liteorm.MsgExec,
		slog.String(liteorm.AttrSQL, "DELETE FROM t WHERE id = ?"),
		slog.Int64(liteorm.AttrRows, 3),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
		slog.Any(liteorm.AttrError, "boom"),
	))
	out := buf.String()
	if !strings.Contains(out, "rows=3") || !strings.Contains(out, "ERROR: boom") {
		t.Errorf("exec line missing rows/error: %q", out)
	}
}

func TestColorEnabled(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, &Options{Color: true})
	_ = h.Handle(context.Background(), record(liteorm.MsgQuery,
		slog.String(liteorm.AttrSQL, "SELECT 1"),
		slog.Duration(liteorm.AttrDur, time.Millisecond),
	))
	if !strings.Contains(buf.String(), "\033[") {
		t.Error("Color:true must emit ANSI codes")
	}
}

func TestLevelGate(t *testing.T) {
	// The zero Level defaults to Debug (statement logs are at Debug), so a plain
	// dev handler shows them; raising the floor hides them.
	if !NewHandler(nil, nil).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("default handler must enable Debug")
	}
	if !NewHandler(nil, &Options{Color: false}).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("a color-only override must still enable Debug")
	}
	if NewHandler(nil, &Options{Level: slog.LevelWarn}).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Level=Warn must disable Debug")
	}
}
