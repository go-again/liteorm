package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

func TestCount(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	for i := range 5 {
		mustCreate(t, &User{Name: "count-me", Active: i%2 == 0})
	}
	if n, _ := repo.Where("name = ?", "count-me").Count(ctx); n != 5 {
		t.Errorf("Count = %d, want 5", n)
	}
	if n, _ := repo.Where("name = ?", "count-me").Filter(query.Col[bool]("active").Eq(true)).Count(ctx); n != 3 {
		t.Errorf("Count(active) = %d, want 3", n)
	}
}

func TestGroupedCountViaInto(t *testing.T) {
	ctx := context.Background()
	for i := range 5 {
		mustCreate(t, &User{Name: "grp-count", Active: i%2 == 0}) // 3 active, 2 inactive
	}
	type byActive struct {
		Active bool  `db:"active"`
		N      int64 `db:"n"`
	}
	rows, err := query.Into[User, byActive](ctx,
		query.Select[User](DB).
			Where("name = ?", "grp-count").
			GroupByCols(query.Col[bool]("active").Field()).
			Order(query.Asc(query.Col[bool]("active"))),
		query.Col[bool]("active").Field(),
		query.CountAs(query.Col[int64]("id"), "n"))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[bool]int64{}
	for _, r := range rows {
		counts[r.Active] = r.N
	}
	if counts[true] != 3 || counts[false] != 2 {
		t.Errorf("grouped counts = %v, want true:3 false:2", counts)
	}
}
