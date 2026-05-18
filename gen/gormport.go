package gen

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"liteorm.org/internal/scan"
)

// This is a source-to-source porter that rewrites gorm struct tags into
// liteorm's native orm tags. It is not a runtime emulator. It rewrites
// `gorm:"…"` struct tags into liteorm's
// native `orm:"…"` tags so a gorm codebase can drop the gorm.io/gorm dependency
// and keep idiomatic, liteorm-native models. It works purely on Go source (AST),
// so it imports no gorm runtime.
//
// liteorm's orm package already reads gorm tags directly, so a ported model and
// the original behave identically; the port is about cleanliness and dropping
// the dependency. Two things the porter rewrites or flags specially: the
// gorm.DeletedAt field type (rewritten to sql.NullTime + a soft_delete tag) and
// the gorm.Model embed (reported with the suggested explicit fields).

// Note is one porter observation tied to a source position.
type Note struct {
	Pos     string // "line:col"
	Message string
}

type edit struct {
	start, end int
	text       string
}

// PortSource rewrites every gorm struct tag in src into a liteorm orm tag and
// returns the modified source plus a report. Only tag literals (and gorm.DeletedAt
// field types) are edited; all other formatting is preserved byte-for-byte.
func PortSource(src []byte) ([]byte, []Note, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("gen: parsing source to port: %w", err)
	}

	var notes []Note
	var edits []edit
	rewroteType := false
	pos := func(p token.Pos) string {
		pp := fset.Position(p)
		return fmt.Sprintf("%d:%d", pp.Line, pp.Column)
	}
	off := func(p token.Pos) int { return fset.Position(p).Offset }

	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 { // embedded
				if sel, ok := field.Type.(*ast.SelectorExpr); ok && isGorm(sel, "Model") {
					notes = append(notes, Note{pos(field.Pos()),
						`replace embedded gorm.Model with explicit fields: ID int64; ` +
							`CreatedAt time.Time ` + "`" + `orm:"created_at,autocreatetime"` + "`" + `; ` +
							`UpdatedAt time.Time ` + "`" + `orm:"updated_at,autoupdatetime"` + "`" + `; ` +
							`DeletedAt sql.NullTime ` + "`" + `orm:"deleted_at,soft_delete"` + "`"})
				}
				continue
			}
			fieldName := field.Names[0].Name

			softDelete := false
			if sel, ok := field.Type.(*ast.SelectorExpr); ok && isGorm(sel, "DeletedAt") {
				edits = append(edits, edit{off(field.Type.Pos()), off(field.Type.End()), "sql.NullTime"})
				softDelete = true
				rewroteType = true
				notes = append(notes, Note{pos(field.Type.Pos()),
					`field type gorm.DeletedAt rewritten to sql.NullTime (ensure the "database/sql" import is present)`})
			}

			gormContent, hasGorm := "", false
			if field.Tag != nil {
				if raw, uerr := strconv.Unquote(field.Tag.Value); uerr == nil {
					gormContent, hasGorm = reflect.StructTag(raw).Lookup("gorm")
				}
			}
			if !hasGorm && !softDelete {
				continue
			}

			ormContent, ns := portGormContent(fieldName, gormContent, softDelete)
			for _, m := range ns {
				notes = append(notes, Note{pos(field.Pos()), m})
			}
			newTag := rewriteTag(field.Tag, ormContent)
			switch {
			case field.Tag != nil:
				edits = append(edits, edit{off(field.Tag.Pos()), off(field.Tag.End()), newTag})
			case newTag != "": // soft_delete from type, no prior tag
				edits = append(edits, edit{off(field.Type.End()), off(field.Type.End()), " " + newTag})
			}
		}
		return true
	})

	if rewroteType {
		notes = append(notes, Note{"imports",
			"run `goimports -w` on the result: gorm.io/gorm is likely now unused and database/sql is required"})
	}

	out := append([]byte(nil), src...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		out = append(out[:e.start:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	// Re-gofmt so the rewritten tags realign; fall back to the raw bytes if the
	// edits somehow produced unformattable source (they shouldn't).
	if formatted, ferr := format.Source(out); ferr == nil {
		out = formatted
	}
	return out, notes, nil
}

// portGormContent translates one gorm tag's content into orm tag content,
// supplying the snake_case column name from fieldName when gorm gives none.
func portGormContent(fieldName, gormContent string, softDelete bool) (string, []string) {
	var column string
	var opts []string
	var notes []string
	add := func(o string) { opts = append(opts, o) }

	for part := range strings.SplitSeq(gormContent, ";") {
		k, v, hasV := strings.Cut(strings.TrimSpace(part), ":")
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "":
		case "-":
			return "-", notes
		case "column":
			column = v
		case "primarykey", "primary_key":
			add("pk")
		case "autoincrement":
			add("autoincrement")
		case "unique", "uniqueindex":
			add("unique")
		case "not null", "notnull":
			add("notnull")
		case "default":
			add("default:" + v)
		case "type":
			add("type:" + v)
		case "size":
			add("size:" + v)
		case "check":
			add("check:" + v)
		case "autocreatetime":
			add("autocreatetime")
		case "autoupdatetime":
			add("autoupdatetime")
		case "embedded":
			add("embedded")
		case "embeddedprefix":
			add("embeddedprefix:" + v)
		case "many2many":
			add("m2m:" + v)
		case "foreignkey":
			add("fk:" + v)
		case "references":
			add("references:" + v)
		case "joinforeignkey":
			add("joinfk:" + v)
		case "joinreferences":
			add("joinref:" + v)
		case "index":
			if v != "" {
				add("index:" + v)
			} else {
				add("index")
			}
		case "->":
			if v == "false" {
				add("readonly")
			}
		case "<-":
			if v == "false" {
				add("writeonly")
			}
		default:
			kept := strings.ToLower(strings.TrimSpace(k))
			if hasV {
				kept += ":" + v
			}
			notes = append(notes, fmt.Sprintf("dropped unsupported gorm tag key %q", kept))
		}
	}
	if softDelete {
		add("soft_delete")
	}
	if column == "" && len(opts) == 0 {
		return "", notes // tag reduces to the default-derived column; drop it
	}
	if column == "" {
		column = scan.Snake(fieldName)
	}
	return strings.Join(append([]string{column}, opts...), ","), notes
}

// rewriteTag rebuilds the raw struct-tag literal (with backticks), replacing the
// gorm key with orm:"ormContent" and preserving any other keys (json, …). When
// ormContent is empty the gorm key is dropped.
func rewriteTag(tag *ast.BasicLit, ormContent string) string {
	var inner string
	if tag != nil {
		inner, _ = strconv.Unquote(tag.Value)
	}
	out := make([]string, 0, 4)
	wroteOrm := false
	for _, p := range parseStructTagPairs(inner) {
		switch p.key {
		case "gorm":
			if ormContent != "" {
				out = append(out, fmt.Sprintf("orm:%q", ormContent))
				wroteOrm = true
			}
		default:
			out = append(out, fmt.Sprintf("%s:%q", p.key, p.value))
			if p.key == "orm" {
				wroteOrm = true
			}
		}
	}
	if !wroteOrm && ormContent != "" {
		out = append(out, fmt.Sprintf("orm:%q", ormContent))
	}
	if len(out) == 0 {
		return ""
	}
	return "`" + strings.Join(out, " ") + "`"
}

type tagPair struct{ key, value string }

// parseStructTagPairs splits a raw struct tag (no backticks) into ordered
// key:"value" pairs, mirroring reflect's own tag scanner.
func parseStructTagPairs(tag string) []tagPair {
	var out []tagPair
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}
		i = 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		key := tag[:i]
		tag = tag[i+1:] // drop key and ':'
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		value, err := strconv.Unquote(tag[:i+1])
		tag = tag[i+1:]
		if err != nil {
			break
		}
		out = append(out, tagPair{key, value})
	}
	return out
}

func isGorm(sel *ast.SelectorExpr, name string) bool {
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gorm" && sel.Sel.Name == name
}
