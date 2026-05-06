package query

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

func TestCTERenders(t *testing.T) {
	pg := mockSession{d: sqlgen.Postgres}
	sub := Select[tuser](pg).Filter(Col[string]("email").Eq("a@b.c")) // CTE body, binds first
	q, args, err := Select[tuser](pg).
		With("active", sub).
		From("active").
		Filter(Col[int]("age").Gt(18)).
		buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q, `WITH "active" AS (SELECT `) {
		t.Errorf("missing WITH prefix: %q", q)
	}
	// The CTE body's bind numbers before the outer query's (global numbering).
	if !strings.Contains(q, `"email" = $1`) || !strings.Contains(q, `"age" > $2`) {
		t.Errorf("placeholder ordering across CTE/main wrong: %q", q)
	}
	// The main query selects from the CTE, columns re-qualified to it.
	if !strings.Contains(q, `) SELECT "active"."id"`) || !strings.HasSuffix(q, `FROM "active" WHERE "age" > $2`) {
		t.Errorf("main FROM the CTE wrong: %q", q)
	}
	if len(args) != 2 || args[0] != "a@b.c" || args[1] != 18 {
		t.Errorf("args = %v, want [a@b.c 18]", args)
	}
}

func TestRecursiveCTERenders(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	anchor := Select[tuser](sess).Where("age IS NULL")
	recur := Select[tuser](sess).Join("JOIN tree ON tree.id = users.age")
	q, _, err := Select[tuser](sess).
		WithRecursive("tree", anchor.UnionAll(recur)).
		From("tree").
		buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q, `WITH RECURSIVE "tree" AS (`) || !strings.Contains(q, " UNION ALL SELECT ") {
		t.Errorf("recursive CTE: %q", q)
	}
}

func TestCTEAllDialectsQuote(t *testing.T) {
	cases := []struct {
		d      dialect.Dialect
		prefix string
	}{
		{sqlgen.SQLite, `WITH "t" AS (`},
		{sqlgen.Postgres, `WITH "t" AS (`},
		{sqlgen.MySQL, "WITH `t` AS ("},
		{sqlgen.MSSQL, `WITH [t] AS (`},
	}
	for _, c := range cases {
		sess := mockSession{d: c.d}
		q, _, err := Select[tuser](sess).With("t", Select[tuser](sess)).From("t").buildSQL()
		if err != nil {
			t.Fatalf("%s: %v", c.d.Name(), err)
		}
		if !strings.HasPrefix(q, c.prefix) {
			t.Errorf("%s: %q missing %q", c.d.Name(), q, c.prefix)
		}
	}
}

func TestFromSubqueryRenders(t *testing.T) {
	pg := mockSession{d: sqlgen.Postgres}
	sub := Select[tuser](pg).Filter(Col[int]("age").Gt(18))
	q, args, err := FromSubquery[tuser](pg, "d", sub).Filter(Col[string]("name").Eq("x")).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, `FROM (SELECT `) || !strings.Contains(q, `) AS "d"`) {
		t.Errorf("derived table FROM wrong: %q", q)
	}
	if !strings.HasPrefix(q, `SELECT "d"."id"`) {
		t.Errorf("columns not qualified to alias: %q", q)
	}
	if len(args) != 2 || args[0] != 18 || args[1] != "x" {
		t.Errorf("args = %v, want [18 x]", args)
	}
}

func TestJoinSubRenders(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	sub := Select[tuser](sess).Project("id").Filter(Col[int]("age").Gt(18))
	q, args, err := Select[tuser](sess).
		JoinSub("INNER JOIN", "j", sub, "j.id = users.id").
		Where("users.name = ?", "x").
		buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	// Project is the raw select-list escape hatch, so "id" renders verbatim.
	if !strings.Contains(q, `INNER JOIN (SELECT id FROM "users" WHERE "age" > ?) AS "j" ON j.id = users.id`) {
		t.Errorf("join-subquery wrong: %q", q)
	}
	if len(args) != 2 || args[0] != 18 || args[1] != "x" {
		t.Errorf("args = %v, want [18 x]", args)
	}
}

func TestJoinLateralGated(t *testing.T) {
	pg := mockSession{d: sqlgen.Postgres}
	sub := Select[tuser](pg).Project("id").Where("orders.uid = users.id")
	q, _, err := Select[tuser](pg).JoinLateral("LEFT JOIN", "j", sub, "true").buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, `LEFT JOIN LATERAL (SELECT id`) {
		t.Errorf("lateral join: %q", q)
	}
	for _, d := range []dialect.Dialect{sqlgen.SQLite, sqlgen.MySQL, sqlgen.MSSQL} {
		s := mockSession{d: d}
		ls := Select[tuser](s).Project("id")
		if _, _, err := Select[tuser](s).JoinLateral("LEFT JOIN", "j", ls, "true").buildSQL(); err == nil {
			t.Errorf("%s: LATERAL should error", d.Name())
		}
	}
}
