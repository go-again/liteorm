package query

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

func TestTypedOrderBy(t *testing.T) {
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `ORDER BY "age" DESC, "name" ASC`},
		{sqlgen.Postgres, `ORDER BY "age" DESC, "name" ASC`},
		{sqlgen.MySQL, "ORDER BY `age` DESC, `name` ASC"},
		{sqlgen.MSSQL, `ORDER BY [age] DESC, [name] ASC`},
	}
	for _, c := range cases {
		q, _, err := Select[tuser](mockSession{d: c.d}).
			Order(Desc(Col[int]("age")), Asc(Col[string]("name"))).buildSQL()
		if err != nil {
			t.Fatalf("%s: %v", c.d.Name(), err)
		}
		if !strings.HasSuffix(q, c.want) {
			t.Errorf("%s: got %q, want suffix %q", c.d.Name(), q, c.want)
		}
	}
}

func TestTypedGroupBy(t *testing.T) {
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `GROUP BY "age"`},
		{sqlgen.Postgres, `GROUP BY "age"`},
		{sqlgen.MySQL, "GROUP BY `age`"},
		{sqlgen.MSSQL, `GROUP BY [age]`},
	}
	for _, c := range cases {
		q, _, err := Select[tuser](mockSession{d: c.d}).
			GroupByCols(Col[int]("age").Field()).buildSQL()
		if err != nil {
			t.Fatalf("%s: %v", c.d.Name(), err)
		}
		if !strings.HasSuffix(q, c.want) {
			t.Errorf("%s: got %q, want suffix %q", c.d.Name(), q, c.want)
		}
	}
	// GROUP BY composes with a raw HAVING.
	q, _, err := Select[tuser](mockSession{d: sqlgen.SQLite}).
		GroupByCols(Col[int]("age").Field()).Having("count(*) > ?", 1).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(q, `GROUP BY "age" HAVING count(*) > ?`) {
		t.Errorf("group+having: %q", q)
	}
}

func TestMixedRawAndTypedOrder(t *testing.T) {
	// Raw and typed terms compose in call order: raw "email" verbatim, typed quoted.
	q, _, err := Select[tuser](mockSession{d: sqlgen.SQLite}).
		OrderBy("email").Order(Desc(Col[int]("age"))).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(q, `ORDER BY email, "age" DESC`) {
		t.Errorf("mixed order: %q", q)
	}
}

func TestTypedClauseUnknownColumn(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	if _, _, err := Select[tuser](sess).Order(Asc(Col[int]("nope"))).buildSQL(); err == nil {
		t.Error("Order with an unknown column should error")
	}
	if _, _, err := Select[tuser](sess).GroupByCols(Name("nope")).buildSQL(); err == nil {
		t.Error("GroupByCols with an unknown column should error")
	}
}

func TestAggFieldRender(t *testing.T) {
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `SUM("total") AS "revenue"`},
		{sqlgen.MySQL, "SUM(`total`) AS `revenue`"},
		{sqlgen.MSSQL, `SUM([total]) AS [revenue]`},
	}
	for _, c := range cases {
		if got, _ := SumAs(Col[int]("total"), "revenue").render(c.d); got != c.want {
			t.Errorf("%s: SumAs = %q, want %q", c.d.Name(), got, c.want)
		}
	}
	// Raw Expr passes through verbatim and is not validated.
	if got, _ := Expr("count(*) AS n").render(sqlgen.SQLite); got != "count(*) AS n" {
		t.Errorf("Expr render = %q", got)
	}
}
