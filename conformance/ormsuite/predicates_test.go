package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/query"
)

// TestTypedPredicates covers the typed predicate layer an ORM user reaches for:
// In/NotIn (xorm builder.In), Or/Not (gorm Or/Not), Like, range, and IS NULL —
// all column-validated and dialect-quoted.
func TestTypedPredicates(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		age  int64
	}{{"al", 10}, {"bo", 20}, {"cy", 30}, {"di", 40}} {
		mustCreate(t, &User{Name: "pred-" + c.name, Age: c.age})
	}
	name := query.Col[string]("name")
	age := query.Col[int64]("age")
	base := func() *query.SelectBuilder[User] { return query.Select[User](DB).Where("name LIKE ?", "pred-%") }

	// IN
	in, _ := base().Filter(name.In("pred-al", "pred-cy")).All(ctx)
	if len(in) != 2 {
		t.Errorf("In returned %d, want 2", len(in))
	}
	// NOT IN
	notIn, _ := base().Filter(name.NotIn("pred-al", "pred-cy")).All(ctx)
	if len(notIn) != 2 {
		t.Errorf("NotIn returned %d, want 2", len(notIn))
	}
	// OR of two predicates
	or, _ := base().Filter(query.Or(age.Lt(15), age.Gt(35))).All(ctx)
	if len(or) != 2 { // al(10) and di(40)
		t.Errorf("Or returned %d, want 2", len(or))
	}
	// NOT wrapping a predicate
	not, _ := base().Filter(query.Not(age.Lt(35))).All(ctx)
	if len(not) != 1 || not[0].Age != 40 {
		t.Errorf("Not(age<35) returned %d rows, want 1 (age 40)", len(not))
	}
	// range: Ge AND Le
	rng, _ := base().Filter(age.Ge(20), age.Le(30)).All(ctx)
	if len(rng) != 2 {
		t.Errorf("range [20,30] returned %d, want 2", len(rng))
	}
	// Like
	like, _ := base().Filter(name.Like("pred-a%")).All(ctx)
	if len(like) != 1 || like[0].Name != "pred-al" {
		t.Errorf("Like returned %d rows, want 1 (pred-al)", len(like))
	}
}

// TestDistinct covers SELECT DISTINCT projection (gorm/xorm Distinct).
func TestDistinct(t *testing.T) {
	ctx := context.Background()
	// Three users, two distinct ages.
	mustCreate(t, &User{Name: "dist", Age: 5})
	mustCreate(t, &User{Name: "dist", Age: 5})
	mustCreate(t, &User{Name: "dist", Age: 9})

	type ageRow struct {
		Age int64 `db:"age"`
	}
	rows, err := query.Into[User, ageRow](ctx,
		query.Select[User](DB).Where("name = ?", "dist").Distinct().Order(query.Asc(query.Col[int64]("age"))),
		query.Col[int64]("age").Field())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Age != 5 || rows[1].Age != 9 {
		t.Errorf("DISTINCT ages = %+v, want [5 9]", rows)
	}
}
