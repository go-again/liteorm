// Package sqlgen generates SQL for liteorm's core. It is built around a Dialect
// interface plus a Feature capability bitmask and clause-assembly logic, and
// always emits BOUND placeholders (never inlined literals). The four dialect
// renderers live in dialects.go; all four render correctly under
// sqlgen_test.go, and SQLite is the dialect wired to a live DB.
package sqlgen

import (
	"strconv"
	"strings"

	"liteorm.org/dialect"
)

// Column is a (optionally table-qualified) identifier in a SELECT list. Both
// parts are quoted via the dialect; Table is omitted when empty.
type Column struct {
	Table string
	Name  string
}

func (c Column) appendTo(b []byte, d dialect.Dialect) []byte {
	if c.Table != "" {
		b = d.QuoteIdent(b, c.Table)
		b = append(b, '.')
	}
	return d.QuoteIdent(b, c.Name)
}

// Expr is a raw SQL fragment carrying positional "?" markers plus their args.
// The builder rewrites each "?" to the dialect's placeholder and appends args
// in order — this is how a single "?"-based fragment renders as $n (pg) or
// @pN (mssql).
type Expr struct {
	SQL  string
	Args []any
}

// Select is a SELECT statement model. Identifiers in Columns and Table are
// quoted; Joins/Where/OrderBy/GroupBy are raw SQL fragments (Joins/Where/Having
// may carry "?"). When Projection is non-empty it is the raw select list (used
// for count(*) and projections), overriding Columns. Union holds compound arms;
// the outer Select's ORDER BY/LIMIT apply to the whole compound.
type Select struct {
	With         []CTE   // WITH common table expressions, prepended to the statement
	Table        string  // the FROM table (quoted); ignored when FromSubquery is set
	FromSubquery *Select // a derived-table FROM source: FROM (sub) AS FromAlias
	FromAlias    string
	Columns      []Column
	Projection   []string // a raw select list (verbatim, no binds); overridden by ProjectionExprs
	// ProjectionExprs is a select list whose items may carry "?" binds (e.g. a
	// scalar subquery in SELECT). It renders before FROM so its binds number first;
	// it takes precedence over Projection and Columns.
	ProjectionExprs []Expr
	Distinct        bool
	DistinctOn      []string // SELECT DISTINCT ON (cols) — Postgres; overrides Distinct
	Joins           []Expr
	Where           []Expr
	GroupBy         []string
	Having          []Expr
	OrderBy         []string
	Limit           int
	Offset          int
	HasLimit        bool
	HasOffset       bool
	Union           []CompoundTerm
	Lock            *Lock // row-level locking (FOR UPDATE/SHARE), applied to the whole statement
}

// CTE is one named common table expression in a WITH clause. Recursive marks the
// whole WITH as RECURSIVE (the keyword is emitted once if any CTE sets it).
type CTE struct {
	Name      string
	Recursive bool
	Select    Select
}

// CompoundTerm is one arm of a compound select. Op is the set operator
// ("UNION", "INTERSECT", "EXCEPT"; empty means UNION); All keeps duplicates.
type CompoundTerm struct {
	Op     string
	All    bool
	Select Select
}

// Lock models SELECT row locking. Strength is "UPDATE" or "SHARE"; at most one of
// SkipLocked / NoWait applies (NoWait wins if both are set).
type Lock struct {
	Strength   string
	SkipLocked bool
	NoWait     bool
}

// appendFragment copies raw SQL into b, rewriting each '?' to the dialect
// placeholder for parameter *n (1-based) and advancing *n. String-literal
// awareness is intentionally omitted (fragments are simple).
func appendFragment(b []byte, d dialect.Dialect, frag string, n *int) []byte {
	for i := 0; i < len(frag); i++ {
		if frag[i] == '?' {
			b = d.AppendPlaceholder(b, *n)
			*n++
			continue
		}
		b = append(b, frag[i])
	}
	return b
}

// Build renders s for the dialect, returning the SQL and ordered args. When s
// has Union arms it renders `core UNION [ALL] core …` and applies the outer
// ORDER BY/LIMIT to the whole compound. Placeholder numbering (n) is global
// across the join, where, having, and every union arm — so a single "?"-based
// model renders as $1,$2,… in order on Postgres.
func (s Select) Build(d dialect.Dialect) (string, []any, error) {
	var b []byte
	var args []any
	n := 1

	b, args = s.appendWith(b, args, d, &n)
	b, args = s.appendSelect(b, args, d, &n)
	return string(b), args, nil
}

// appendWith renders the leading WITH [RECURSIVE] clause, if any, threading the
// shared placeholder counter so a CTE body's binds number before the main query's.
func (s Select) appendWith(b []byte, args []any, d dialect.Dialect, n *int) ([]byte, []any) {
	if len(s.With) == 0 {
		return b, args
	}
	b = append(b, "WITH "...)
	if s.anyRecursive() {
		b = append(b, "RECURSIVE "...)
	}
	for i, c := range s.With {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.QuoteIdent(b, c.Name)
		b = append(b, " AS ("...)
		b, args = c.Select.appendSelect(b, args, d, n)
		b = append(b, ')')
	}
	b = append(b, ' ')
	return b, args
}

func (s Select) anyRecursive() bool {
	for _, c := range s.With {
		if c.Recursive {
			return true
		}
	}
	return false
}

// appendSelect renders the core SELECT, its compound (UNION/INTERSECT/EXCEPT)
// arms, and the trailing ORDER BY/LIMIT/lock — everything except a leading WITH.
// It is reused to render CTE bodies and derived-table subqueries inline, so their
// placeholders flow through the same global counter n.
func (s Select) appendSelect(b []byte, args []any, d dialect.Dialect, n *int) ([]byte, []any) {
	b, args = s.appendCore(b, args, d, n)
	for _, u := range s.Union {
		op := u.Op
		if op == "" {
			op = "UNION"
		}
		b = append(b, ' ')
		b = append(b, op...)
		b = append(b, ' ')
		if u.All {
			b = append(b, "ALL "...)
		}
		b, args = u.Select.appendCore(b, args, d, n)
	}
	b = s.appendTail(b, d)
	return b, args
}

// appendCore renders SELECT…HAVING (the part each compound arm carries) using
// the shared running placeholder counter n.
func (s Select) appendCore(b []byte, args []any, d dialect.Dialect, n *int) ([]byte, []any) {
	b = append(b, "SELECT "...)
	switch {
	case len(s.DistinctOn) > 0:
		b = append(b, "DISTINCT ON ("...)
		b = append(b, strings.Join(s.DistinctOn, ", ")...)
		b = append(b, ") "...)
	case s.Distinct:
		b = append(b, "DISTINCT "...)
	}
	switch {
	case len(s.ProjectionExprs) > 0:
		for i, e := range s.ProjectionExprs {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = appendFragment(b, d, e.SQL, n)
			args = append(args, e.Args...)
		}
	case len(s.Projection) > 0:
		b = append(b, strings.Join(s.Projection, ", ")...)
	case len(s.Columns) == 0:
		b = append(b, '*')
	default:
		for i, c := range s.Columns {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = c.appendTo(b, d)
		}
	}

	b = append(b, " FROM "...)
	if s.FromSubquery != nil {
		b = append(b, '(')
		b, args = s.FromSubquery.appendSelect(b, args, d, n)
		b = append(b, ") AS "...)
		b = d.QuoteIdent(b, s.FromAlias)
	} else {
		b = d.QuoteIdent(b, s.Table)
	}

	for _, j := range s.Joins {
		b = append(b, ' ')
		b = appendFragment(b, d, j.SQL, n)
		args = append(args, j.Args...)
	}

	b, args = appendAndList(b, d, " WHERE ", s.Where, n, args)

	if len(s.GroupBy) > 0 {
		b = append(b, " GROUP BY "...)
		b = append(b, strings.Join(s.GroupBy, ", ")...)
	}

	b, args = appendAndList(b, d, " HAVING ", s.Having, n, args)
	return b, args
}

// appendAndList renders "<keyword> a AND b AND …" for a list of bind-bearing
// fragments, threading the shared placeholder counter. It is the single place
// WHERE and HAVING clauses are assembled (for SELECT, UPDATE, and DELETE).
func appendAndList(b []byte, d dialect.Dialect, keyword string, exprs []Expr, n *int, args []any) ([]byte, []any) {
	if len(exprs) == 0 {
		return b, args
	}
	b = append(b, keyword...)
	for i, e := range exprs {
		if i > 0 {
			b = append(b, " AND "...)
		}
		b = appendFragment(b, d, e.SQL, n)
		args = append(args, e.Args...)
	}
	return b, args
}

// appendTail renders ORDER BY / LIMIT / OFFSET / locking, which bind to the whole
// compound.
func (s Select) appendTail(b []byte, d dialect.Dialect) []byte {
	if len(s.OrderBy) > 0 {
		b = append(b, " ORDER BY "...)
		b = append(b, strings.Join(s.OrderBy, ", ")...)
	} else if (s.HasLimit || s.HasOffset) && d.Features().Has(dialect.FeatOffsetFetch) {
		// OFFSET…FETCH requires an ORDER BY (MSSQL); use a no-op order when none given.
		b = append(b, " ORDER BY (SELECT NULL)"...)
	}
	b = appendLimitOffset(b, d, s.HasLimit, s.Limit, s.HasOffset, s.Offset)
	return appendLock(b, s.Lock)
}

// appendLock renders FOR UPDATE / FOR SHARE [SKIP LOCKED | NOWAIT]. Dialect
// support is gated by the builder (FeatRowLocking), so this only renders for a
// dialect that accepts the syntax.
func appendLock(b []byte, lock *Lock) []byte {
	if lock == nil {
		return b
	}
	b = append(b, " FOR "...)
	b = append(b, lock.Strength...)
	switch {
	case lock.NoWait:
		b = append(b, " NOWAIT"...)
	case lock.SkipLocked:
		b = append(b, " SKIP LOCKED"...)
	}
	return b
}

// appendLimitOffset renders LIMIT/OFFSET, switching to OFFSET..FETCH for
// dialects that advertise FeatOffsetFetch (MSSQL). Bounds are inlined as
// integer literals (safe, and the common convention).
func appendLimitOffset(b []byte, d dialect.Dialect, hasLimit bool, limit int, hasOffset bool, offset int) []byte {
	if d.Features().Has(dialect.FeatOffsetFetch) {
		if !hasLimit && !hasOffset {
			return b
		}
		off := 0
		if hasOffset {
			off = offset
		}
		b = append(b, " OFFSET "...)
		b = strconv.AppendInt(b, int64(off), 10)
		b = append(b, " ROWS"...)
		if hasLimit {
			b = append(b, " FETCH NEXT "...)
			b = strconv.AppendInt(b, int64(limit), 10)
			b = append(b, " ROWS ONLY"...)
		}
		return b
	}
	if hasLimit {
		b = append(b, " LIMIT "...)
		b = strconv.AppendInt(b, int64(limit), 10)
	}
	if hasOffset {
		b = append(b, " OFFSET "...)
		b = strconv.AppendInt(b, int64(offset), 10)
	}
	return b
}
