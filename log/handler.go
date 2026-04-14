// Package log provides a colored, human-readable slog.Handler tuned for
// liteorm's statement logs: one line per executed statement with timing, the
// SQL, its bind arguments, the rows affected, and the Go source location that
// issued it — so you can watch every query during development and trace it back
// to the line of code. Wire it in with liteorm.WithLogger(log.New(os.Stderr,
// nil)). It also renders ordinary (non-statement) slog records as a plain line,
// so it can serve as a development logger for the whole program.
//
// For structured/production logging use a standard slog handler instead
// (slog.NewJSONHandler / slog.NewTextHandler) — liteorm logs the same statement
// events through whichever handler is configured.
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	liteorm "liteorm.org"
)

// Options configures the handler. A nil *Options uses the defaults.
type Options struct {
	Level         slog.Level    // minimum level (default slog.LevelDebug — statement logs are emitted at debug)
	Color         bool          // ANSI color (default true; forced off when the NO_COLOR env var is set)
	SlowThreshold time.Duration // statements at or over this are highlighted (default 200ms; 0 disables)
	AbsPath       bool          // print the absolute caller path; by default it is shown relative to the working directory
}

// New returns an *slog.Logger backed by the colored statement handler.
func New(w io.Writer, opts *Options) *slog.Logger { return slog.New(NewHandler(w, opts)) }

// NewHandler returns the colored statement handler writing to w.
func NewHandler(w io.Writer, opts *Options) slog.Handler {
	o := Options{Level: slog.LevelDebug, Color: true, SlowThreshold: 200 * time.Millisecond}
	if opts != nil {
		o = *opts
		if o.Level == 0 {
			o.Level = slog.LevelDebug
		}
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		o.Color = false
	}
	cwd, _ := os.Getwd()
	return &handler{w: w, mu: &sync.Mutex{}, opt: o, cwd: cwd}
}

type handler struct {
	w     io.Writer
	mu    *sync.Mutex
	opt   Options
	cwd   string // working directory, for relativizing the caller path
	attrs []slog.Attr
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.opt.Level }

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	// This handler renders statement events on one line and doesn't namespace
	// attributes under groups, so a group is a structural no-op.
	return h
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == liteorm.MsgQuery || r.Message == liteorm.MsgExec {
		return h.writeLine(h.statementLine(r))
	}
	return h.writeLine(h.plainLine(r))
}

// statementLine renders a liteorm.query / liteorm.exec event as one colored line.
func (h *handler) statementLine(r slog.Record) string {
	var sql, callerStr, errStr string
	var dur time.Duration
	var rows int64 = -1
	var argsList []any
	var argsCount int
	var argsRedacted bool
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case liteorm.AttrSQL:
			sql = a.Value.String()
		case liteorm.AttrDur:
			dur = a.Value.Duration()
		case liteorm.AttrCaller:
			callerStr = a.Value.String()
		case liteorm.AttrError:
			errStr = a.Value.String()
		case liteorm.AttrRows:
			rows = a.Value.Int64()
		case liteorm.AttrArgs:
			// args are the bind values ([]any) or, when redacted, their count (int).
			if a.Value.Kind() == slog.KindInt64 {
				argsCount, argsRedacted = int(a.Value.Int64()), true
			} else if as, ok := a.Value.Any().([]any); ok {
				argsList = as
			}
		}
		return true
	})

	c := colors{enabled: h.opt.Color}
	var b strings.Builder
	b.WriteString(c.dim(r.Time.Format("15:04:05.000")))
	b.WriteByte(' ')
	b.WriteString(c.cyan("[liteorm]"))
	b.WriteByte(' ')

	durFn := c.green
	switch {
	case errStr != "":
		durFn = c.red
	case h.opt.SlowThreshold > 0 && dur >= h.opt.SlowThreshold:
		durFn = c.yellow
	}
	b.WriteString(durFn(fmtDur(dur)))
	b.WriteString("  ")
	b.WriteString(collapseWS(sql))

	switch {
	case argsRedacted && argsCount > 0:
		b.WriteByte(' ')
		b.WriteString(c.dim("args=<" + strconv.Itoa(argsCount) + " hidden>"))
	case len(argsList) > 0:
		b.WriteByte(' ')
		b.WriteString(c.dim("args=" + formatArgs(argsList)))
	}
	if rows >= 0 {
		b.WriteByte(' ')
		b.WriteString(c.dim("rows=" + strconv.FormatInt(rows, 10)))
	}
	if callerStr != "" {
		b.WriteByte(' ')
		b.WriteString(c.dim("(" + h.callerPath(callerStr) + ")"))
	}
	if errStr != "" {
		b.WriteString("\n  ")
		b.WriteString(c.red("ERROR: " + errStr))
	}
	return b.String()
}

func (h *handler) plainLine(r slog.Record) string {
	c := colors{enabled: h.opt.Color}
	var b strings.Builder
	b.WriteString(c.dim(r.Time.Format("15:04:05.000")))
	b.WriteByte(' ')
	b.WriteString(levelTag(c, r.Level))
	b.WriteByte(' ')
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(c.dim(a.Key + "=" + a.Value.String()))
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	return b.String()
}

func (h *handler) writeLine(s string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, s+"\n")
	return err
}

func levelTag(c colors, l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return c.red("ERROR")
	case l >= slog.LevelWarn:
		return c.yellow("WARN")
	case l >= slog.LevelInfo:
		return c.green("INFO")
	default:
		return c.dim("DEBUG")
	}
}

// callerPath shortens an absolute "file:line" to a path relative to the working
// directory — the default, so a statement traces to e.g. (examples/blog/main.go:42)
// rather than a long absolute path. AbsPath keeps it absolute. The structured
// caller attribute stays absolute for JSON/text handlers; this is purely the
// human handler's presentation.
func (h *handler) callerPath(s string) string {
	if h.opt.AbsPath || h.cwd == "" {
		return s
	}
	// Split a trailing ":<line>" — and only that, so a Windows drive colon
	// ("C:\x\main.go") is never mistaken for the line separator.
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || !allDigits(s[i+1:]) {
		return s
	}
	rel, err := filepath.Rel(h.cwd, s[:i])
	// Fall back to the absolute path when the file is outside the working
	// directory (Rel yields a long "../../.." that is uglier, not shorter).
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return s
	}
	return rel + s[i:]
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// formatArgs renders bind args with each value delimited and strings quoted, so a
// value containing spaces is unambiguous: [42, "ada lovelace", NULL].
func formatArgs(args []any) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		switch v := a.(type) {
		case nil:
			b.WriteString("NULL")
		case string:
			b.WriteString(strconv.Quote(v))
		case []byte:
			b.WriteString(strconv.Quote(string(v)))
		default:
			fmt.Fprintf(&b, "%v", v)
		}
	}
	b.WriteByte(']')
	return b.String()
}

// collapseWS squeezes runs of whitespace (including newlines from multi-line
// DDL) into single spaces so a statement renders on one line.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + "s"
	case d >= time.Millisecond:
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64) + "ms"
	case d >= time.Microsecond:
		return strconv.FormatFloat(float64(d)/float64(time.Microsecond), 'f', 0, 64) + "µs"
	default:
		return strconv.FormatInt(int64(d), 10) + "ns"
	}
}

// colors renders ANSI-colored text when enabled, plain text otherwise.
type colors struct{ enabled bool }

func (c colors) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func (c colors) dim(s string) string    { return c.wrap("2", s) }
func (c colors) red(s string) string    { return c.wrap("31", s) }
func (c colors) green(s string) string  { return c.wrap("32", s) }
func (c colors) yellow(s string) string { return c.wrap("33", s) }
func (c colors) cyan(s string) string   { return c.wrap("36", s) }

var _ slog.Handler = (*handler)(nil)
