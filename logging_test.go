package liteorm

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"liteorm.org/internal/sqlgen"
)

// capHandler records every slog.Record it handles, always debug-enabled.
type capHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capHandler) WithGroup(string) slog.Handler      { return h }

func loggedArgs(t *testing.T, r slog.Record) []any {
	t.Helper()
	var args []any
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == AttrArgs {
			args, _ = a.Value.Any().([]any)
		}
		return true
	})
	return args
}

// TestLogArgsCapped proves large blob/text bind args are bounded in the log: a
// big string is truncated with a byte-count marker, a big []byte becomes a size
// summary, and small args pass through untouched.
func TestLogArgsCapped(t *testing.T) {
	h := &capHandler{}
	db := New(&fakeConn{}, sqlgen.SQLite, WithLogger(slog.New(h)))

	bigText := strings.Repeat("x", 5000)
	bigBlob := make([]byte, 4000)
	if _, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES (?,?,?)", 7, bigText, bigBlob); err != nil {
		t.Fatal(err)
	}

	if len(h.recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(h.recs))
	}
	args := loggedArgs(t, h.recs[0])
	if len(args) != 3 {
		t.Fatalf("logged %d args, want 3", len(args))
	}
	if args[0] != 7 {
		t.Errorf("small arg changed: %v, want 7", args[0])
	}
	s, ok := args[1].(string)
	if !ok || len(s) >= len(bigText) || !strings.Contains(s, "(5000 bytes)") {
		t.Errorf("big text not truncated: %q", s)
	}
	if args[2] != "<4000 bytes>" {
		t.Errorf("big blob not summarized: %v", args[2])
	}
}

// TestLogArgsRedactedSkipsValues confirms WithSQLArgs(false) logs only the count,
// so no value (large or small) is rendered.
func TestLogArgsRedactedSkipsValues(t *testing.T) {
	h := &capHandler{}
	db := New(&fakeConn{}, sqlgen.SQLite, WithLogger(slog.New(h)), WithSQLArgs(false))
	if _, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES (?,?)", "a", "b"); err != nil {
		t.Fatal(err)
	}
	var kind slog.Kind
	h.recs[0].Attrs(func(a slog.Attr) bool {
		if a.Key == AttrArgs {
			kind = a.Value.Kind()
		}
		return true
	})
	if kind != slog.KindInt64 {
		t.Errorf("redacted args should be an int count, got kind %v", kind)
	}
}
