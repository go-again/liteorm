package query

import (
	"strings"
	"testing"

	"liteorm.org/internal/sqlgen"
)

// Tests for the typed predicates/fields added to retire raw Where/Set/Expr
// fragments (the pantry "zero raw SQL" filing): Match, HasPrefix/HasSuffix/
// Contains, EqCol, ExistsField, and Update.Inc/Dec.

func TestMatchPredicate(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}
	pg := mockSession{d: sqlgen.Postgres}

	q, args, err := Select[tuser](lite).Filter(Match("name", "rocket")).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(q, `WHERE "name" MATCH ?`) {
		t.Errorf("sqlite match: %s", q)
	}
	if len(args) != 1 || args[0] != "rocket" {
		t.Errorf("args = %v, want [rocket]", args)
	}

	// MATCH is SQLite-only — the feature gate rejects it elsewhere, even nested.
	if _, _, err := Select[tuser](pg).Filter(Match("name", "x")).buildSQL(); err == nil {
		t.Error("Match on postgres should be rejected by the feature gate")
	}
	if _, _, err := Select[tuser](pg).Filter(And(Col[int64]("id").Gt(0), Match("name", "x"))).buildSQL(); err == nil {
		t.Error("nested Match on postgres should still be rejected")
	}
}

func TestLikeEscapingPredicates(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}
	cases := []struct {
		name string
		pred Predicate
		arg  string
	}{
		{"prefix", Col[string]("name").HasPrefix("100%"), "100~%%"},
		{"suffix", Col[string]("name").HasSuffix("%off"), "%~%off"},
		{"contains", Col[string]("name").Contains("a_b"), "%a~_b%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, args, err := Select[tuser](lite).Filter(c.pred).buildSQL()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(q, "WHERE \"name\" LIKE ? ESCAPE '~'") {
				t.Errorf("sql = %s", q)
			}
			if len(args) != 1 || args[0] != c.arg {
				t.Errorf("arg = %v, want %q", args, c.arg)
			}
		})
	}
}

func TestEqColPredicate(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}
	q, _, err := Select[tuser](lite).
		Filter(Col[int64]("id").EqCol(Col[int64]("id").Of("orders"))).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(q, `WHERE "id" = "orders"."id"`) {
		t.Errorf("eqcol: %s", q)
	}
}

func TestExistsFieldRender(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}
	f := ExistsField("has_order",
		Select[tuser](lite).Filter(Col[int64]("id").EqCol(Col[int64]("id").Of("u"))))
	if f.err != nil {
		t.Fatalf("construction error: %v", f.err)
	}
	s, _ := f.render(sqlgen.SQLite)
	if !strings.HasPrefix(s, "CASE WHEN EXISTS (") || !strings.HasSuffix(s, `END AS "has_order"`) {
		t.Errorf("exists field render: %s", s)
	}
}

func TestUnvalidatedColumn(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}

	// "scope" is not a column on tuser; without Unvalidated the validator rejects
	// it, with Unvalidated it renders quoted and survives validation.
	if _, _, err := Select[tuser](lite).Filter(Col[int]("scope").Le(2)).buildSQL(); err == nil {
		t.Error("a validated predicate on an unknown column should be rejected")
	}
	q, args, err := Select[tuser](lite).Filter(Col[int]("scope").Unvalidated().Le(2)).buildSQL()
	if err != nil {
		t.Fatalf("unvalidated predicate rejected: %v", err)
	}
	if !strings.HasSuffix(q, `WHERE "scope" <= ?`) {
		t.Errorf("unvalidated render: %s", q)
	}
	if len(args) != 1 || args[0] != 2 {
		t.Errorf("args = %v, want [2]", args)
	}

	// Nested inside And, alongside a validated predicate, it still passes.
	if _, _, err := Select[tuser](lite).
		Filter(And(Col[int64]("id").Gt(0), Col[int]("scope").Unvalidated().Eq(1))).buildSQL(); err != nil {
		t.Errorf("unvalidated predicate nested in And rejected: %v", err)
	}

	// Unvalidated column as an ORDER BY term is not validated either.
	if _, _, err := Select[tuser](lite).Order(Asc(Col[int64]("rowid").Unvalidated())).buildSQL(); err != nil {
		t.Errorf("unvalidated order term rejected: %v", err)
	}
	if _, _, err := Select[tuser](lite).Order(Asc(Col[int64]("rowid"))).buildSQL(); err == nil {
		t.Error("a validated order term on an unknown column should be rejected")
	}
}

func TestUpdateIncDecRender(t *testing.T) {
	lite := mockSession{d: sqlgen.SQLite}
	up, err := Update[tuser](lite).Inc("age", 1).Where("id = ?", 1).resolved()
	if err != nil {
		t.Fatal(err)
	}
	q, args, err := up.Build(sqlgen.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, `SET "age" = "age" + ?`) {
		t.Errorf("inc: %s", q)
	}
	if len(args) != 2 || args[0] != int64(1) {
		t.Errorf("inc args = %v, want [1 1]", args)
	}

	up, _ = Update[tuser](lite).Dec("age", 2).Where("id = ?", 1).resolved()
	q, _, _ = up.Build(sqlgen.SQLite)
	if !strings.Contains(q, `SET "age" = "age" - ?`) {
		t.Errorf("dec: %s", q)
	}
}
