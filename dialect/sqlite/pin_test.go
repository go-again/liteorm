package sqlite_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// recHandler records statement events, always debug-enabled.
type recHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *recHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.recs = append(h.recs, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *recHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recHandler) WithGroup(string) slog.Handler      { return h }

// TestPin_InheritsLogging proves a pinned session logs through the source DB's
// configured logger and honors its WithSQLArgs setting — not slog.Default() with
// values exposed (the bug where Pin built its handle without the DB's options).
func TestPin_InheritsLogging(t *testing.T) {
	ctx := context.Background()
	rec := &recHandler{}
	db, err := sqlite.Open(
		filepath.Join(t.TempDir(), "pin.db"),
		liteorm.WithLogger(slog.New(rec)),
		liteorm.WithSQLArgs(false), // redact arg values
	)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bound, _, release, err := sqlite.Pin(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := bound.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.ExecContext(ctx, `INSERT INTO t (v) VALUES (?)`, "secret"); err != nil {
		t.Fatal(err)
	}

	if len(rec.recs) == 0 {
		t.Fatal("pinned session did not log through the configured logger")
	}
	// The INSERT's args must be redacted to a count (WithSQLArgs(false) inherited),
	// so the value "secret" never reaches the log.
	redacted := false
	for _, r := range rec.recs {
		if r.Message != liteorm.MsgExec {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == liteorm.AttrArgs && a.Value.Kind() == slog.KindInt64 {
				redacted = true
			}
			if a.Key == liteorm.AttrArgs {
				if as, ok := a.Value.Any().([]any); ok {
					for _, v := range as {
						if v == "secret" {
							t.Error("pinned session leaked a redacted arg value")
						}
					}
				}
			}
			return true
		})
	}
	if !redacted {
		t.Fatal("pinned session did not honor the inherited WithSQLArgs(false)")
	}
}
