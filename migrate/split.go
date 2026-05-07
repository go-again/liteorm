package migrate

import "strings"

// splitStatements splits a migration body into individual SQL statements on
// top-level ';', so each runs as its own Exec (MySQL/MSSQL can't always run a
// multi-statement Exec). It is quote- and comment-aware: it ignores ';' inside
// single-quoted strings, quoted identifiers ("..." / `...` / [...]), line
// (`--`) and block (`/* */`) comments, and Postgres dollar-quoted bodies
// ($tag$...$tag$), so function/trigger definitions survive intact.
func splitStatements(sql string) []string {
	var stmts []string
	var b strings.Builder
	r := []rune(sql)
	n := len(r)

	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			stmts = append(stmts, s)
		}
		b.Reset()
	}

	for i := 0; i < n; {
		c := r[i]
		switch {
		case c == '-' && i+1 < n && r[i+1] == '-':
			for i < n && r[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && r[i+1] == '*':
			i += 2
			for i+1 < n && (r[i] != '*' || r[i+1] != '/') {
				i++
			}
			i += 2
			b.WriteRune(' ') // keep a token boundary where the comment was
		case c == '\'':
			b.WriteRune(c)
			i++
			for i < n {
				b.WriteRune(r[i])
				if r[i] == '\'' {
					if i+1 < n && r[i+1] == '\'' { // escaped ''
						b.WriteRune(r[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			b.WriteRune(c)
			i++
			for i < n {
				b.WriteRune(r[i])
				if r[i] == closer {
					i++
					break
				}
				i++
			}
		case c == '$':
			if tag, ok := dollarTag(r, i); ok {
				b.WriteString(tag)
				i += len([]rune(tag))
				for i < n {
					if r[i] == '$' && hasRunePrefix(r[i:], tag) {
						b.WriteString(tag)
						i += len([]rune(tag))
						break
					}
					b.WriteRune(r[i])
					i++
				}
			} else {
				b.WriteRune(c)
				i++
			}
		case c == ';':
			i++
			flush()
		default:
			b.WriteRune(c)
			i++
		}
	}
	flush()
	return stmts
}

// dollarTag reads an opening Postgres dollar-quote tag ("$$" or "$name$") at r[i].
func dollarTag(r []rune, i int) (string, bool) {
	j := i + 1
	for j < len(r) && (isWordRune(r[j])) {
		j++
	}
	if j < len(r) && r[j] == '$' {
		return string(r[i : j+1]), true
	}
	return "", false
}

func hasRunePrefix(r []rune, prefix string) bool {
	p := []rune(prefix)
	if len(r) < len(p) {
		return false
	}
	for i := range p {
		if r[i] != p[i] {
			return false
		}
	}
	return true
}

func isWordRune(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
