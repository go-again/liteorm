package gen

import (
	"fmt"
	"strings"
)

// This file holds the codegen mechanics shared between liteorm's own annotated-SQL
// generator (queries.go) and the sqlc-gen-liteorm plugin: the per-verb function
// body, the import decision, scalar detection, and identifier casing. The two
// generators differ only in how they obtain a query's result type and parameters
// (annotations vs sqlc's typed catalog) — the emitted shape is identical, so it
// lives here once.

// scalarTypes are result types scanned directly from a single column rather than
// mapped into a struct (which is what query.Raw expects).
var scalarTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "byte": true, "rune": true,
	"float32": true, "float64": true, "string": true, "bool": true,
	"[]byte": true, "time.Time": true, "any": true,
}

// IsScalar reports whether result is a single-column scalar (or slice) rather
// than a generated/named row struct.
func IsScalar(result string) bool {
	t := strings.TrimPrefix(result, "*")
	if strings.HasPrefix(t, "[]") && t != "[]byte" {
		return true
	}
	return scalarTypes[t]
}

// NeedsQueryImport reports whether a query needs the liteorm.org/query import:
// only struct-returning :one/:many do (scalars scan directly, exec uses the
// session). Getting this right is what keeps an all-scalar file compilable.
func NeedsQueryImport(cmd QueryCmd, result string) bool {
	return (cmd == CmdOne || cmd == CmdMany) && !IsScalar(result)
}

// EmitFunc writes the const + function for one query into b. params is the full
// parameter list (including "ctx context.Context, sess liteorm.Session") and
// callArgs is the leading-comma argument list passed to the SQL call.
func EmitFunc(b *strings.Builder, name string, cmd QueryCmd, constName, result, params, callArgs string) {
	switch cmd {
	case CmdMany:
		fmt.Fprintf(b, "func %s(%s) ([]%s, error) {\n", name, params, result)
		if IsScalar(result) {
			fmt.Fprintf(b, "\trows, err := sess.QueryContext(ctx, %s%s)\n", constName, callArgs)
			b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n\tdefer func() { _ = rows.Close() }()\n")
			fmt.Fprintf(b, "\tvar out []%s\n\tfor rows.Next() {\n\t\tvar v %s\n", result, result)
			b.WriteString("\t\tif err := rows.Scan(&v); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tout = append(out, v)\n\t}\n\treturn out, rows.Err()\n}\n\n")
		} else {
			fmt.Fprintf(b, "\treturn query.Raw[%s](ctx, sess, %s%s)\n}\n\n", result, constName, callArgs)
		}
	case CmdOne:
		fmt.Fprintf(b, "func %s(%s) (%s, error) {\n\tvar zero %s\n", name, params, result, result)
		if IsScalar(result) {
			fmt.Fprintf(b, "\trows, err := sess.QueryContext(ctx, %s%s)\n", constName, callArgs)
			b.WriteString("\tif err != nil {\n\t\treturn zero, err\n\t}\n\tdefer func() { _ = rows.Close() }()\n")
			b.WriteString("\tif !rows.Next() {\n\t\tif err := rows.Err(); err != nil {\n\t\t\treturn zero, err\n\t\t}\n\t\treturn zero, liteorm.ErrNoRows\n\t}\n")
			b.WriteString("\tif err := rows.Scan(&zero); err != nil {\n\t\treturn zero, err\n\t}\n\treturn zero, nil\n}\n\n")
		} else {
			fmt.Fprintf(b, "\trows, err := query.Raw[%s](ctx, sess, %s%s)\n", result, constName, callArgs)
			b.WriteString("\tif err != nil {\n\t\treturn zero, err\n\t}\n\tif len(rows) == 0 {\n\t\treturn zero, liteorm.ErrNoRows\n\t}\n\treturn rows[0], nil\n}\n\n")
		}
	case CmdExecRows:
		fmt.Fprintf(b, "func %s(%s) (int64, error) {\n\tres, err := sess.ExecContext(ctx, %s%s)\n", name, params, constName, callArgs)
		b.WriteString("\tif err != nil {\n\t\treturn 0, err\n\t}\n\treturn res.RowsAffected(), nil\n}\n\n")
	case CmdExecResult:
		fmt.Fprintf(b, "func %s(%s) (liteorm.Result, error) {\n\treturn sess.ExecContext(ctx, %s%s)\n}\n\n", name, params, constName, callArgs)
	case CmdExecLastID:
		fmt.Fprintf(b, "func %s(%s) (int64, error) {\n\tres, err := sess.ExecContext(ctx, %s%s)\n", name, params, constName, callArgs)
		b.WriteString("\tif err != nil {\n\t\treturn 0, err\n\t}\n\tli, ok := res.(liteorm.LastInsertIder)\n\tif !ok {\n\t\treturn 0, errors.New(\"liteorm: backend does not report LastInsertId\")\n\t}\n\treturn li.LastInsertId()\n}\n\n")
	default: // CmdExec
		fmt.Fprintf(b, "func %s(%s) error {\n\t_, err := sess.ExecContext(ctx, %s%s)\n\treturn err\n}\n\n", name, params, constName, callArgs)
	}
}

// LowerFirst lowercases the first letter of s.
func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// Initialism upper-cases a known initialism segment, else title-cases it.
func Initialism(p string) string {
	switch strings.ToUpper(p) {
	case "ID", "URL", "API", "UUID", "SQL", "DB", "JSON", "HTTP", "IP":
		return strings.ToUpper(p)
	}
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
}

// GoName converts a snake_case / dotted column name to an exported Go identifier,
// upper-casing common initialisms (e.g. "user_id" → "UserID"). An empty result
// falls back to "Col".
func GoName(s string) string {
	var b strings.Builder
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == ' ' || r == '.' }) {
		b.WriteString(Initialism(p))
	}
	if b.Len() == 0 {
		return "Col"
	}
	return b.String()
}
