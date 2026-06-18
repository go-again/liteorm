package liteorm

import (
	"context"
	"log/slog"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Statement logging. liteorm logs every executed SQL statement through the
// configured *slog.Logger at debug level, so logging is off unless the logger is
// enabled for slog.LevelDebug. Each event carries the SQL, the bind args (or
// their count), the elapsed time, rows affected (for Exec), the originating Go
// source location, and the error if any — enough to trace any statement back to
// the line of Go that issued it. Pair it with the colored/plain handler in
// liteorm.org/log for human-readable development output, or any slog handler
// (JSON, text, OpenTelemetry bridge) for structured logs.

// Statement-event message strings and attribute keys. A custom slog.Handler can
// match on these to format liteorm's statement logs specially.
const (
	MsgQuery = "liteorm.query" // a QueryContext (SELECT / RETURNING) event
	MsgExec  = "liteorm.exec"  // an ExecContext (INSERT/UPDATE/DELETE/DDL) event

	AttrSQL    = "sql"    // the SQL text, with the dialect's placeholders
	AttrArgs   = "args"   // the bind args ([]any) or, when redacted, their count (int)
	AttrDur    = "dur"    // time.Duration the statement took
	AttrRows   = "rows"   // int64 rows affected (Exec only; absent for queries)
	AttrCaller = "caller" // "file:line" of the Go code that issued the statement
	AttrError  = "err"    // the error, if the statement failed
)

// logStmt emits a statement event. The caller has already confirmed the logger
// is debug-enabled, so the source-location walk only runs when something will
// consume it.
func logStmt(ctx context.Context, log *slog.Logger, msg, query string, args []any, logArgs bool, start time.Time, rows int64, err error) {
	attrs := make([]slog.Attr, 0, 6)
	attrs = append(attrs, slog.String(AttrSQL, query), slog.Duration(AttrDur, time.Since(start)))
	if logArgs {
		attrs = append(attrs, slog.Any(AttrArgs, args))
	} else {
		attrs = append(attrs, slog.Int(AttrArgs, len(args)))
	}
	if rows >= 0 {
		attrs = append(attrs, slog.Int64(AttrRows, rows))
	}
	if c := caller(); c != "" {
		attrs = append(attrs, slog.String(AttrCaller, c))
	}
	if err != nil {
		attrs = append(attrs, slog.Any(AttrError, err))
	}
	log.LogAttrs(ctx, slog.LevelDebug, msg, attrs...)
}

// sourceDir is the directory of liteorm's own source tree (the dir of this
// file), used to recognize and skip liteorm's own stack frames. The runtime
// embeds source paths with forward slashes on every OS (including Windows), so
// this uses path, not filepath — filepath.Dir would yield backslashes on Windows
// and the HasPrefix checks below would never match.
var sourceDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return path.Dir(file) + "/"
}()

// caller returns the "file:line" of the first stack frame outside liteorm's own
// source — the user code that issued the statement. The path-based stack walk is
// ported from gorm's logger (utils.FileWithLineNum / callerFrame): a frame
// counts as the caller when it lives outside liteorm's source dir, or is a test
// file, or is an example program (so liteorm's own tests/examples report their
// own line rather than being skipped).
func caller() string {
	var pcs [20]uintptr
	n := runtime.Callers(2, pcs[:]) // skip runtime.Callers and caller itself
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if f.File != "" && isUserFrame(f.File) {
			return f.File + ":" + strconv.Itoa(f.Line)
		}
		if !more {
			break
		}
	}
	return ""
}

func isUserFrame(file string) bool {
	if !strings.HasPrefix(file, sourceDir) {
		return true // outside the liteorm source tree
	}
	if strings.HasSuffix(file, "_test.go") {
		return true // liteorm's own tests are the "user" of the API
	}
	return strings.HasPrefix(file, sourceDir+"examples/")
}
