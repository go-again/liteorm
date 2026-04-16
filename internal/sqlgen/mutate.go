package sqlgen

import (
	"fmt"

	"liteorm.org/dialect"
)

// Conflict is the upsert spec for an INSERT. Columns are the conflict-target
// columns; Update are the columns to overwrite on conflict.
type Conflict struct {
	Columns []string
	Update  []string
}

// Insert is an INSERT statement model with optional multi-row VALUES, upsert,
// and RETURNING/OUTPUT. Identifiers are quoted; values bind as placeholders.
type Insert struct {
	Table      string
	Columns    []string
	Rows       [][]any
	OnConflict *Conflict
	Returning  []string
}

// Build renders ins for the dialect. Upsert syntax and the returning-clause
// shape are selected from the dialect's Feature bits, then the clause is
// assembled here.
func (ins Insert) Build(d dialect.Dialect) (string, []any, error) {
	if len(ins.Rows) == 0 {
		return "", nil, fmt.Errorf("sqlgen: INSERT into %q has no rows", ins.Table)
	}
	if len(ins.Columns) == 0 {
		return "", nil, fmt.Errorf("sqlgen: INSERT into %q has no columns", ins.Table)
	}
	for ri, row := range ins.Rows {
		if len(row) != len(ins.Columns) {
			return "", nil, fmt.Errorf("sqlgen: INSERT into %q row %d has %d values, want %d columns", ins.Table, ri, len(row), len(ins.Columns))
		}
	}
	// MSSQL has no INSERT…ON CONFLICT; an upsert is a MERGE.
	if ins.OnConflict != nil && d.Features().Has(dialect.FeatMerge) {
		return ins.buildMerge(d)
	}
	feat := d.Features()
	var b []byte
	var args []any
	n := 1

	b = append(b, "INSERT INTO "...)
	b = d.QuoteIdent(b, ins.Table)
	b = append(b, " ("...)
	for i, c := range ins.Columns {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.QuoteIdent(b, c)
	}
	b = append(b, ')')

	// MSSQL OUTPUT goes between the column list and VALUES.
	if len(ins.Returning) > 0 && feat.Has(dialect.FeatOutput) {
		b = appendOutput(b, d, ins.Returning, "INSERTED.")
	}

	b = append(b, " VALUES "...)
	for ri, row := range ins.Rows {
		if ri > 0 {
			b = append(b, ", "...)
		}
		b = append(b, '(')
		for ci := range row {
			if ci > 0 {
				b = append(b, ", "...)
			}
			b = d.AppendPlaceholder(b, n)
			n++
		}
		b = append(b, ')')
		args = append(args, row...)
	}

	if ins.OnConflict != nil {
		var err error
		if b, err = appendUpsert(b, d, ins.OnConflict); err != nil {
			return "", nil, err
		}
	}

	// RETURNING at statement end (pg/sqlite). OUTPUT was already emitted above.
	if len(ins.Returning) > 0 && feat.Has(dialect.FeatReturning) {
		b = appendReturning(b, d, ins.Returning)
	}
	return string(b), args, nil
}

// buildMerge renders an upsert as a T-SQL MERGE (MSSQL), with OUTPUT for the
// returning columns.
func (ins Insert) buildMerge(d dialect.Dialect) (string, []any, error) {
	c := ins.OnConflict
	var b []byte
	var args []any
	n := 1
	b = append(b, "MERGE INTO "...)
	b = d.QuoteIdent(b, ins.Table)
	b = append(b, " AS tgt USING (VALUES "...)
	for ri, row := range ins.Rows {
		if ri > 0 {
			b = append(b, ", "...)
		}
		b = append(b, '(')
		for ci := range row {
			if ci > 0 {
				b = append(b, ", "...)
			}
			b = d.AppendPlaceholder(b, n)
			n++
		}
		b = append(b, ')')
		args = append(args, row...)
	}
	b = append(b, ") AS src ("...)
	b = appendCols(b, d, ins.Columns)
	b = append(b, ") ON "...)
	for i, col := range c.Columns {
		if i > 0 {
			b = append(b, " AND "...)
		}
		b = append(b, "tgt."...)
		b = d.QuoteIdent(b, col)
		b = append(b, " = src."...)
		b = d.QuoteIdent(b, col)
	}
	b = append(b, " WHEN MATCHED THEN UPDATE SET "...)
	for i, col := range c.Update {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, "tgt."...)
		b = d.QuoteIdent(b, col)
		b = append(b, " = src."...)
		b = d.QuoteIdent(b, col)
	}
	b = append(b, " WHEN NOT MATCHED THEN INSERT ("...)
	b = appendCols(b, d, ins.Columns)
	b = append(b, ") VALUES ("...)
	for i, col := range ins.Columns {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, "src."...)
		b = d.QuoteIdent(b, col)
	}
	b = append(b, ')')
	if len(ins.Returning) > 0 {
		b = appendOutput(b, d, ins.Returning, "INSERTED.")
	}
	b = append(b, ';')
	return string(b), args, nil
}

// appendOutput renders a T-SQL OUTPUT clause (e.g. `OUTPUT INSERTED."col"`); the
// prefix selects the pseudo-table ("INSERTED." or "DELETED.").
func appendOutput(b []byte, d dialect.Dialect, cols []string, prefix string) []byte {
	b = append(b, " OUTPUT "...)
	for i, c := range cols {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, prefix...)
		b = d.QuoteIdent(b, c)
	}
	return b
}

func appendCols(b []byte, d dialect.Dialect, cols []string) []byte {
	for i, col := range cols {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.QuoteIdent(b, col)
	}
	return b
}

func appendUpsert(b []byte, d dialect.Dialect, c *Conflict) ([]byte, error) {
	feat := d.Features()
	switch {
	case feat.Has(dialect.FeatInsertOnConflict):
		b = append(b, " ON CONFLICT ("...)
		for i, col := range c.Columns {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = d.QuoteIdent(b, col)
		}
		b = append(b, ") DO UPDATE SET "...)
		for i, col := range c.Update {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = d.QuoteIdent(b, col)
			b = append(b, " = EXCLUDED."...)
			b = d.QuoteIdent(b, col)
		}
		return b, nil
	case feat.Has(dialect.FeatOnDuplicateKey):
		b = append(b, " ON DUPLICATE KEY UPDATE "...)
		for i, col := range c.Update {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = d.QuoteIdent(b, col)
			b = append(b, " = VALUES("...)
			b = d.QuoteIdent(b, col)
			b = append(b, ')')
		}
		return b, nil
	default:
		// MSSQL MERGE-based upsert is handled separately via buildMerge.
		return nil, fmt.Errorf("sqlgen: dialect %q has no upsert support", d.Name())
	}
}

func appendReturning(b []byte, d dialect.Dialect, cols []string) []byte {
	b = append(b, " RETURNING "...)
	for i, c := range cols {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.QuoteIdent(b, c)
	}
	return b
}

// SetClause is one column assignment in an UPDATE.
// SetClause assigns a column. By default it binds Arg as a placeholder
// (col = ?); when SQL is non-empty it is a raw assignment expression (col = SQL,
// which may carry "?" markers bound by Args) — used for correlated UPDATE … FROM
// where the value comes from the FROM source.
type SetClause struct {
	Column string
	Arg    any
	SQL    string
	Args   []any
}

// Update is an UPDATE statement model. From, when set, is a correlated
// `FROM <source>` fragment (UPDATE … FROM); it may carry "?" markers.
type Update struct {
	Table     string
	Set       []SetClause
	From      *Expr
	Where     []Expr
	Returning []string
}

// Build renders u. Args bind in clause order: SET, then FROM, then WHERE.
func (u Update) Build(d dialect.Dialect) (string, []any, error) {
	if len(u.Set) == 0 {
		return "", nil, fmt.Errorf("sqlgen: UPDATE %q has no SET clauses", u.Table)
	}
	var b []byte
	var args []any
	n := 1

	b = append(b, "UPDATE "...)
	b = d.QuoteIdent(b, u.Table)
	b = append(b, " SET "...)
	for i, s := range u.Set {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = d.QuoteIdent(b, s.Column)
		b = append(b, " = "...)
		if s.SQL != "" {
			b = appendFragment(b, d, s.SQL, &n)
			args = append(args, s.Args...)
		} else {
			b = d.AppendPlaceholder(b, n)
			n++
			args = append(args, s.Arg)
		}
	}
	// T-SQL OUTPUT sits after SET and before FROM; RETURNING goes at the end.
	if len(u.Returning) > 0 && d.Features().Has(dialect.FeatOutput) {
		b = appendOutput(b, d, u.Returning, "INSERTED.")
	}
	if u.From != nil {
		b = append(b, " FROM "...)
		b = appendFragment(b, d, u.From.SQL, &n)
		args = append(args, u.From.Args...)
	}
	b, args = appendWhere(b, d, u.Where, &n, args)
	if len(u.Returning) > 0 && d.Features().Has(dialect.FeatReturning) {
		b = appendReturning(b, d, u.Returning)
	}
	return string(b), args, nil
}

// Delete is a DELETE statement model.
type Delete struct {
	Table     string
	Where     []Expr
	Returning []string
}

// Build renders del.
func (del Delete) Build(d dialect.Dialect) (string, []any, error) {
	var b []byte
	var args []any
	n := 1
	b = append(b, "DELETE FROM "...)
	b = d.QuoteIdent(b, del.Table)
	// T-SQL OUTPUT (of the DELETED rows) sits before WHERE; RETURNING at the end.
	if len(del.Returning) > 0 && d.Features().Has(dialect.FeatOutput) {
		b = appendOutput(b, d, del.Returning, "DELETED.")
	}
	b, args = appendWhere(b, d, del.Where, &n, args)
	if len(del.Returning) > 0 && d.Features().Has(dialect.FeatReturning) {
		b = appendReturning(b, d, del.Returning)
	}
	return string(b), args, nil
}

func appendWhere(b []byte, d dialect.Dialect, where []Expr, n *int, args []any) ([]byte, []any) {
	return appendAndList(b, d, " WHERE ", where, n, args)
}
