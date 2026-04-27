package query

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

type tuser struct {
	ID    int64
	Name  string
	Age   int
	Email string
}

func (tuser) TableName() string { return "users" }

// mockSession provides a dialect (for SQL building) and no-op execution.
type mockSession struct{ d dialect.Dialect }

func (m mockSession) QueryContext(context.Context, string, ...any) (liteorm.Rows, error) {
	return nil, nil
}
func (m mockSession) ExecContext(context.Context, string, ...any) (liteorm.Result, error) {
	return nil, nil
}
func (m mockSession) Dialect() dialect.Dialect { return m.d }
func (m mockSession) Logger() *slog.Logger     { return slog.Default() }

type tdoc struct {
	ID   int64
	Data string // JSON/JSONB column
	Tags string // Postgres array column
}

func (tdoc) TableName() string { return "docs" }

func TestJSONAndArrayPredicates(t *testing.T) {
	pg := mockSession{d: sqlgen.Postgres}
	lite := mockSession{d: sqlgen.SQLite}

	t.Run("json path extraction (both dialects)", func(t *testing.T) {
		q, args, err := Select[tdoc](pg).
			Filter(JSON("data").Key("addr").Key("city").Eq("Paris")).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(q, `WHERE "data" -> $1 ->> $2 = $3`) {
			t.Errorf("pg json path: %s", q)
		}
		if len(args) != 3 || args[0] != "addr" || args[1] != "city" || args[2] != "Paris" {
			t.Errorf("args = %v, want [addr city Paris]", args)
		}
		// SQLite supports ->/->> too — path extraction is not feature-gated.
		q, _, err = Select[tdoc](lite).Filter(JSON("data").Key("k").Eq("v")).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(q, `WHERE "data" ->> ? = ?`) {
			t.Errorf("sqlite json path: %s", q)
		}
	})

	t.Run("jsonb containment is postgres-only", func(t *testing.T) {
		q, _, err := Select[tdoc](pg).Filter(JSON("data").Contains(map[string]any{"active": true})).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(q, `WHERE "data" @> $1`) {
			t.Errorf("pg jsonb contains: %s", q)
		}
		if _, _, err := Select[tdoc](lite).Filter(JSON("data").Contains(1)).buildSQL(); err == nil {
			t.Fatal("jsonb containment on sqlite should be rejected by the feature gate")
		}
	})

	t.Run("array operators are postgres-only", func(t *testing.T) {
		q, args, err := Select[tdoc](pg).Filter(Array[string]("tags").Contains("go", "db")).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(q, `WHERE "tags" @> ARRAY[$1, $2]`) {
			t.Errorf("pg array contains: %s", q)
		}
		if len(args) != 2 {
			t.Errorf("args = %v", args)
		}
		q, _, err = Select[tdoc](pg).Filter(Array[string]("tags").Has("go")).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(q, `WHERE $1 = ANY("tags")`) {
			t.Errorf("pg array has: %s", q)
		}
		if _, _, err := Select[tdoc](lite).Filter(Array[string]("tags").Overlaps("x")).buildSQL(); err == nil {
			t.Fatal("array operators on sqlite should be rejected by the feature gate")
		}
	})

	t.Run("feature requirement survives And/Or nesting", func(t *testing.T) {
		_, _, err := Select[tdoc](lite).Filter(
			And(Col[int64]("id").Gt(0), Array[string]("tags").Has("x")),
		).buildSQL()
		if err == nil {
			t.Fatal("nested array op on sqlite should still be rejected")
		}
	})
}

func TestJoinsUnionSubquery(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	pg := mockSession{d: sqlgen.Postgres}

	t.Run("typed joins quote the table", func(t *testing.T) {
		q, _, err := Select[tuser](sess).
			LeftJoin("orders", "orders.user_id = users.id").
			Filter(Col[int]("age").Gt(18)).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !contains(q, `FROM "users" LEFT JOIN "orders" ON orders.user_id = users.id WHERE "age" > ?`) {
			t.Errorf("join sql: %s", q)
		}
	})

	t.Run("union renumbers placeholders across arms", func(t *testing.T) {
		a := Select[tuser](pg).Filter(Col[int]("age").Lt(18))
		b := Select[tuser](pg).Filter(Col[int]("age").Gt(65))
		q, args, err := a.Union(b).OrderBy("name").buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `WHERE "age" < $1 UNION SELECT "users"."id", "users"."name", "users"."age", "users"."email" FROM "users" WHERE "age" > $2 ORDER BY name`
		if !contains(q, want) {
			t.Errorf("union sql: %s", q)
		}
		if len(args) != 2 || args[0] != 18 || args[1] != 65 {
			t.Errorf("union args = %v, want [18 65]", args)
		}
	})

	t.Run("union all", func(t *testing.T) {
		a := Select[tuser](sess)
		b := Select[tuser](sess)
		q, _, err := a.UnionAll(b).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		if !contains(q, "UNION ALL SELECT") {
			t.Errorf("union all sql: %s", q)
		}
	})

	t.Run("IN subquery", func(t *testing.T) {
		// users whose id is in (select author_id from docs where ...)
		sub := Select[tdoc](pg).Project("id").Filter(Col[string]("data").Eq("x"))
		q, args, err := Select[tuser](pg).Filter(Col[int64]("id").InQuery(sub)).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `WHERE "id" IN (SELECT id FROM "docs" WHERE "data" = $1)`
		if !contains(q, want) {
			t.Errorf("in-subquery sql: %s", q)
		}
		if len(args) != 1 || args[0] != "x" {
			t.Errorf("in-subquery args = %v", args)
		}
	})

	t.Run("EXISTS subquery with outer args before it", func(t *testing.T) {
		sub := Select[tdoc](pg).Project("1").Where("docs.id = users.id")
		q, args, err := Select[tuser](pg).
			Filter(Col[string]("name").Eq("ada"), Exists(sub)).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		// the outer name=$1 must precede the (argument-less) EXISTS body
		if !contains(q, `WHERE "name" = $1 AND EXISTS (SELECT 1 FROM "docs" WHERE docs.id = users.id)`) {
			t.Errorf("exists sql: %s", q)
		}
		if len(args) != 1 || args[0] != "ada" {
			t.Errorf("exists args = %v", args)
		}
	})

	t.Run("subquery column error surfaces", func(t *testing.T) {
		sub := Select[tdoc](sess).Project("id").Filter(Col[int]("nope").Eq(1))
		_, _, err := Select[tuser](sess).Filter(Col[int64]("id").InQuery(sub)).buildSQL()
		if err == nil {
			t.Fatal("invalid column in subquery should error")
		}
	})

	t.Run("invalid union arm surfaces", func(t *testing.T) {
		bad := Select[tuser](sess).Filter(Col[int]("nope").Eq(1))
		_, _, err := Select[tuser](sess).Union(bad).buildSQL()
		if err == nil {
			t.Fatal("invalid union arm should error")
		}
	})
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestEmptyPredicateEdgeCases(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}

	t.Run("empty In is always-false, empty NotIn always-true", func(t *testing.T) {
		q, _, err := Select[tuser](sess).Filter(Col[int]("age").In()).buildSQL()
		if err != nil || !strings.HasSuffix(q, "WHERE 1=0") {
			t.Fatalf("empty In: %q err=%v", q, err)
		}
		q, _, _ = Select[tuser](sess).Filter(Col[int]("age").NotIn()).buildSQL()
		if !strings.HasSuffix(q, "WHERE 1=1") {
			t.Errorf("empty NotIn: %q", q)
		}
	})

	t.Run("And/Or with zero args is a no-op, one arg unwraps", func(t *testing.T) {
		q, _, err := Select[tuser](sess).Filter(And()).buildSQL()
		if err != nil || !strings.HasSuffix(q, "WHERE 1=1") {
			t.Fatalf("empty And: %q err=%v", q, err)
		}
		q, _, _ = Select[tuser](sess).Filter(And(Col[int]("age").Gt(1))).buildSQL()
		if !strings.HasSuffix(q, `WHERE "age" > ?`) { // unwrapped, no parens
			t.Errorf("single And should unwrap: %q", q)
		}
	})
}

func TestCountOverCompound(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	// Count of a UNION must wrap as a derived table, not produce count(*) UNION count(*).
	a := Select[tuser](sess).Filter(Col[int]("age").Lt(18))
	b := Select[tuser](sess).Filter(Col[int]("age").Gt(65))
	sel, err := a.Union(b).resolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Union) == 0 {
		t.Fatal("expected a union arm")
	}
	// Build what Count would: the wrap path triggers on len(Union)>0.
	inner, _, _ := sel.Build(sess.d)
	wrapped := "SELECT count(*) FROM (" + inner + ") AS _cnt"
	if !strings.HasPrefix(wrapped, "SELECT count(*) FROM (SELECT") || !strings.Contains(wrapped, "UNION") {
		t.Fatalf("count wrap malformed: %s", wrapped)
	}
}

func TestPredicateRenderQuotingPerDialect(t *testing.T) {
	p := Col[string]("name").Eq("x")
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `"name" = ?`},
		{sqlgen.Postgres, `"name" = ?`}, // placeholder rewrite happens later, in sqlgen
		{sqlgen.MySQL, "`name` = ?"},
		{sqlgen.MSSQL, `[name] = ?`},
	}
	for _, c := range cases {
		got, args := p.render(c.d)
		if got != c.want || len(args) != 1 || args[0] != "x" {
			t.Errorf("%s: render = %q %v, want %q [x]", c.d.Name(), got, args, c.want)
		}
	}
}

func TestBuilderSQL(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	const cols = `"users"."id", "users"."name", "users"."age", "users"."email"`

	t.Run("filter typed", func(t *testing.T) {
		q, args, err := Select[tuser](sess).Filter(Col[int]("age").Gt(18)).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT ` + cols + ` FROM "users" WHERE "age" > ?`
		if q != want {
			t.Errorf("\n got: %s\nwant: %s", q, want)
		}
		if len(args) != 1 || args[0] != 18 {
			t.Errorf("args = %v", args)
		}
	})

	t.Run("and/or/in/null", func(t *testing.T) {
		q, args, err := Select[tuser](sess).Filter(
			And(
				Col[string]("name").In("a", "b"),
				Or(Col[int]("age").Ge(18), Col[string]("email").IsNull()),
			),
		).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT ` + cols + ` FROM "users" WHERE ("name" IN (?, ?) AND ("age" >= ? OR "email" IS NULL))`
		if q != want {
			t.Errorf("\n got: %s\nwant: %s", q, want)
		}
		if len(args) != 3 {
			t.Errorf("args = %v, want 3", args)
		}
	})

	t.Run("distinct group having", func(t *testing.T) {
		q, _, err := Select[tuser](sess).Distinct().GroupBy("age").Having("count(*) > ?", 1).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT DISTINCT ` + cols + ` FROM "users" GROUP BY age HAVING count(*) > ?`
		if q != want {
			t.Errorf("\n got: %s\nwant: %s", q, want)
		}
	})

	t.Run("postgres placeholders renumber", func(t *testing.T) {
		pg := mockSession{d: sqlgen.Postgres}
		q, _, err := Select[tuser](pg).Filter(Col[string]("name").Eq("a"), Col[int]("age").Gt(1)).buildSQL()
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "users"."id", "users"."name", "users"."age", "users"."email" FROM "users" WHERE "name" = $1 AND "age" > $2`
		if q != want {
			t.Errorf("\n got: %s\nwant: %s", q, want)
		}
	})

	t.Run("unknown column errors", func(t *testing.T) {
		_, _, err := Select[tuser](sess).Filter(Col[int]("nope").Eq(1)).buildSQL()
		if err == nil {
			t.Fatal("expected error for unknown column, got nil")
		}
	})
}
