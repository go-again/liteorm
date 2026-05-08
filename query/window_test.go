package query

import (
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

func TestWindowFuncRender(t *testing.T) {
	region := Col[string]("region").Field()
	amount := Col[int64]("amount")
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `ROW_NUMBER() OVER (PARTITION BY "region" ORDER BY "amount" DESC) AS "rn"`},
		{sqlgen.Postgres, `ROW_NUMBER() OVER (PARTITION BY "region" ORDER BY "amount" DESC) AS "rn"`},
		{sqlgen.MySQL, "ROW_NUMBER() OVER (PARTITION BY `region` ORDER BY `amount` DESC) AS `rn`"},
		{sqlgen.MSSQL, `ROW_NUMBER() OVER (PARTITION BY [region] ORDER BY [amount] DESC) AS [rn]`},
	}
	for _, c := range cases {
		f := RowNumber().Over(Over().PartitionBy(region).OrderBy(Desc(amount)), "rn")
		got, args := f.render(c.d)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.d.Name(), got, c.want)
		}
		if len(args) != 0 {
			t.Errorf("%s: window funcs carry no binds, got %v", c.d.Name(), args)
		}
	}
}

func TestWindowAggAndOffsetRender(t *testing.T) {
	id := Asc(Col[int64]("id"))
	if got, _ := WindowSum(Col[int64]("amount")).Over(Over().OrderBy(id), "running").render(sqlgen.SQLite); got != `SUM("amount") OVER (ORDER BY "id" ASC) AS "running"` {
		t.Errorf("running sum: %q", got)
	}
	if got, _ := Lag(Col[int64]("amount"), 1).Over(Over().OrderBy(id), "prev").render(sqlgen.SQLite); got != `LAG("amount", 1) OVER (ORDER BY "id" ASC) AS "prev"` {
		t.Errorf("lag: %q", got)
	}
}

func TestScalarSubqueryRender(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	sub := Select[tdoc](sess).Project("count(*)").Where("docs.id = users.id")
	got, _ := ScalarSubquery("doc_count", sub).render(sqlgen.SQLite)
	if got != `(SELECT count(*) FROM "docs" WHERE docs.id = users.id) AS "doc_count"` {
		t.Errorf("scalar subquery: %q", got)
	}
	// A bind inside the subquery is carried through (renumbered by the outer build).
	sub2 := Select[tdoc](sess).Project("count(*)").Where("docs.data <> ?", "x")
	_, args := ScalarSubquery("c", sub2).render(sqlgen.SQLite)
	if len(args) != 1 || args[0] != "x" {
		t.Errorf("scalar subquery args = %v, want [x]", args)
	}
}

func TestScalarSubqueryValidatesColumns(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	// An unknown column in the subquery is surfaced through Into's error channel.
	bad := Select[tdoc](sess).Project("count(*)").Filter(Col[int]("nope").Eq(1))
	f := ScalarSubquery("c", bad)
	if f.err == nil {
		t.Error("ScalarSubquery should capture the subquery's column-validation error")
	}
}

func TestScalarSubqueryRejectedInGroupBy(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	// A scalar subquery belongs in Into's projection, not GROUP BY / DISTINCT ON
	// (which can't carry binds). Misuse must error, not silently emit a bind-less
	// placeholder.
	withBind := ScalarSubquery("c", Select[tdoc](sess).Project("count(*)").Where("data <> ?", "z"))
	if _, _, err := Select[tuser](sess).GroupByCols(withBind).buildSQL(); err == nil {
		t.Error("a parameterized scalar subquery in GROUP BY should error")
	}
	// And a subquery construction error surfaces through GROUP BY too (not just Into).
	badCol := ScalarSubquery("c", Select[tdoc](sess).Project("count(*)").Filter(Col[int]("nope").Eq(1)))
	if _, _, err := Select[tuser](sess).DistinctOn(badCol).buildSQL(); err == nil {
		t.Error("a scalar subquery with an unknown column in DISTINCT ON should surface the error")
	}
}
