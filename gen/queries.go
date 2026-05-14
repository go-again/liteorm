package gen

import (
	"fmt"
	"go/format"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// This file is the SQL→typed-Go codegen mode (the schema→models mode lives in
// gen.go). It parses the sqlc annotation grammar — `-- name: Name :cmd` — so
// existing sqlc query files parse unchanged, and emits typed Go functions over
// liteorm's runtime: :one/:many become query.Raw-backed readers, the :exec*
// family become ExecContext wrappers.
//
// This mode does not parse SQL, so result and argument Go types are supplied by
// lightweight companion directives:
//
//	-- name: GetUser :one
//	-- liteorm:result User
//	-- liteorm:arg id int64
//	SELECT id, name FROM users WHERE id = ?;
//
// Result/arg types are emitted verbatim, so they must be valid in the generated
// package (a local type like User, a builtin like int64, or an imported type the
// caller wires up). The :exec family needs no result directive.

// QueryCmd is the sqlc command verb after the colon.
type QueryCmd string

const (
	CmdOne        QueryCmd = "one"        // returns (T, error); ErrNoRows when empty
	CmdMany       QueryCmd = "many"       // returns ([]T, error)
	CmdExec       QueryCmd = "exec"       // returns error
	CmdExecRows   QueryCmd = "execrows"   // returns (int64 affected, error)
	CmdExecResult QueryCmd = "execresult" // returns (liteorm.Result, error)
	CmdExecLastID QueryCmd = "execlastid" // returns (int64 last-insert-id, error)
)

var validCmd = map[QueryCmd]bool{
	CmdOne: true, CmdMany: true, CmdExec: true,
	CmdExecRows: true, CmdExecResult: true, CmdExecLastID: true,
}

// QueryArg is one positional parameter of a query.
type QueryArg struct {
	Name string
	Type string
}

// Query is a single parsed annotated statement.
type Query struct {
	Name   string
	Cmd    QueryCmd
	SQL    string
	Result string // Go result type for :one/:many
	Args   []QueryArg
}

var (
	nameRe   = regexp.MustCompile(`^--\s*name:\s*(\w+)\s+:(\w+)\s*$`)
	resultRe = regexp.MustCompile(`^--\s*liteorm:result\s+(.+?)\s*$`)
	argRe    = regexp.MustCompile(`^--\s*liteorm:arg\s+(\w+)(?:\s+(.+?))?\s*$`)
	dollarRe = regexp.MustCompile(`\$\d+`)
)

// ParseQueries parses sqlc-annotated SQL into Query values, in source order.
func ParseQueries(src string) ([]Query, error) {
	var out []Query
	var cur *Query
	var body []string

	flush := func() error {
		if cur == nil {
			return nil
		}
		cur.SQL = strings.TrimRight(strings.TrimSpace(strings.Join(body, "\n")), ";")
		if err := finalize(cur); err != nil {
			return err
		}
		out = append(out, *cur)
		cur, body = nil, nil
		return nil
	}

	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := nameRe.FindStringSubmatch(trimmed); m != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			cmd := QueryCmd(m[2])
			if !validCmd[cmd] {
				return nil, fmt.Errorf("gen: query %q has unknown command %q", m[1], m[2])
			}
			cur = &Query{Name: m[1], Cmd: cmd}
			continue
		}
		if cur == nil {
			continue // skip preamble before the first -- name: directive
		}
		if m := resultRe.FindStringSubmatch(trimmed); m != nil {
			cur.Result = m[1]
			continue
		}
		if m := argRe.FindStringSubmatch(trimmed); m != nil {
			arg := QueryArg{Name: m[1], Type: m[2]}
			if arg.Type == "" { // single token → it's the type, auto-name it
				arg.Type, arg.Name = m[1], ""
			}
			cur.Args = append(cur.Args, arg)
			continue
		}
		body = append(body, line)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// finalize validates a query and fills in argument names/types from the
// placeholder count when directives are absent.
func finalize(q *Query) error {
	if q.SQL == "" {
		return fmt.Errorf("gen: query %q has no SQL", q.Name)
	}
	if (q.Cmd == CmdOne || q.Cmd == CmdMany) && q.Result == "" {
		return fmt.Errorf("gen: query %q (:%s) needs a `-- liteorm:result <Type>` directive", q.Name, q.Cmd)
	}
	n := placeholderCount(q.SQL)
	for len(q.Args) < n {
		q.Args = append(q.Args, QueryArg{Type: "any"})
	}
	q.Args = q.Args[:n] // ignore surplus directives
	for i := range q.Args {
		if q.Args[i].Name == "" {
			q.Args[i].Name = fmt.Sprintf("arg%d", i+1)
		}
		if q.Args[i].Type == "" {
			q.Args[i].Type = "any"
		}
	}
	return nil
}

// placeholderCount returns the number of bind parameters: distinct $N for
// Postgres-style SQL, else the count of ? markers.
func placeholderCount(sql string) int {
	if ds := dollarRe.FindAllString(sql, -1); len(ds) > 0 {
		hi := 0
		for _, d := range ds {
			n, err := strconv.Atoi(d[1:]) // strip the leading '$'
			if err == nil && n > hi {
				hi = n
			}
		}
		return hi
	}
	return strings.Count(sql, "?")
}

// WriteQueries writes Go source for queries into package pkg.
func WriteQueries(w io.Writer, pkg string, queries []Query) error {
	var needQuery, needErrors bool
	for _, q := range queries {
		if NeedsQueryImport(q.Cmd, q.Result) {
			needQuery = true
		}
		if q.Cmd == CmdExecLastID {
			needErrors = true
		}
	}

	var b strings.Builder
	b.WriteString("// Code generated by liteorm gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("import (\n\t\"context\"\n")
	if needErrors {
		b.WriteString("\t\"errors\"\n")
	}
	b.WriteString("\n\tliteorm \"liteorm.org\"\n")
	if needQuery {
		b.WriteString("\t\"liteorm.org/query\"\n")
	}
	b.WriteString(")\n\n")

	for _, q := range queries {
		emitQuery(&b, q)
	}

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("gen: formatting generated source: %w\n--- source ---\n%s", err, b.String())
	}
	_, err = w.Write(src)
	return err
}

func emitQuery(b *strings.Builder, q Query) {
	constName := LowerFirst(q.Name) + "SQL"
	fmt.Fprintf(b, "const %s = `%s`\n\n", constName, q.SQL)

	var paramsB, callB strings.Builder
	paramsB.WriteString("ctx context.Context, sess liteorm.Session")
	for _, a := range q.Args {
		fmt.Fprintf(&paramsB, ", %s %s", a.Name, a.Type)
		callB.WriteString(", ")
		callB.WriteString(a.Name)
	}
	EmitFunc(b, q.Name, q.Cmd, constName, q.Result, paramsB.String(), callB.String())
}
