package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/query"
)

// TestScalarAggregates covers the whole-set aggregate terminals (xorm's
// Sum/Sums/Count, gorm's aggregate selects).
func TestScalarAggregates(t *testing.T) {
	ctx := context.Background()
	for _, age := range []int64{10, 20, 30, 40} {
		mustCreate(t, &User{Name: "agg", Age: age})
	}
	b := func() *query.SelectBuilder[User] { return query.Select[User](DB).Where("name = ?", "agg") }
	age := query.Col[int64]("age")

	if s, _ := query.Sum(ctx, b(), age); s != 100 {
		t.Errorf("Sum = %d, want 100", s)
	}
	if a, _ := query.Avg(ctx, b(), age); a != 25 {
		t.Errorf("Avg = %v, want 25", a)
	}
	if m, _ := query.Min(ctx, b(), age); m != 10 {
		t.Errorf("Min = %d, want 10", m)
	}
	if m, _ := query.Max(ctx, b(), age); m != 40 {
		t.Errorf("Max = %d, want 40", m)
	}
	if n, _ := query.CountCol(ctx, b(), age); n != 4 {
		t.Errorf("CountCol = %d, want 4", n)
	}
	// An aggregate over no rows yields the zero value, not an error.
	if s, err := query.Sum(ctx, query.Select[User](DB).Where("name = ?", "nobody"), age); err != nil || s != 0 {
		t.Errorf("Sum(empty) = %d err=%v, want 0/nil", s, err)
	}
}

// TestGroupedAggregateHaving covers GROUP BY + aggregate + HAVING (gorm
// group_by/having).
func TestGroupedAggregateHaving(t *testing.T) {
	ctx := context.Background()
	mustCreate(t, &User{Name: "hav", Active: true, Age: 10})
	mustCreate(t, &User{Name: "hav", Active: true, Age: 20})
	mustCreate(t, &User{Name: "hav", Active: false, Age: 1})
	mustCreate(t, &User{Name: "hav", Active: false, Age: 2})

	type sumRow struct {
		Active bool  `db:"active"`
		Total  int64 `db:"total"`
	}
	rows, err := query.Into[User, sumRow](ctx,
		query.Select[User](DB).
			Where("name = ?", "hav").
			GroupByCols(query.Col[bool]("active").Field()).
			Having("SUM(age) > ?", 5),
		query.Col[bool]("active").Field(),
		query.SumAs(query.Col[int64]("age"), "total"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the active group (sum 30) clears HAVING; the inactive group (sum 3) is out.
	if len(rows) != 1 || !rows[0].Active || rows[0].Total != 30 {
		t.Errorf("grouped+having rows = %+v, want one {active:true total:30}", rows)
	}
}

// TestPluckColumn projects a single column into a slice (gorm Pluck / xorm Cols).
func TestPluckColumn(t *testing.T) {
	ctx := context.Background()
	for _, age := range []int64{3, 1, 2} {
		mustCreate(t, &User{Name: "pluck", Age: age})
	}
	age := query.Col[int64]("age")
	got, err := query.Pluck(ctx,
		query.Select[User](DB).Where("name = ?", "pluck").Order(query.Asc(age)),
		age)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("plucked ages = %v, want [1 2 3]", got)
	}
}
