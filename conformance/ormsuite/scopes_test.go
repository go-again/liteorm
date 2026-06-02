package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

func activeOnly(b *query.SelectBuilder[User]) *query.SelectBuilder[User] {
	return b.Where("active = ?", true)
}

func olderThan(age int64) orm.Scope[User] {
	return func(b *query.SelectBuilder[User]) *query.SelectBuilder[User] {
		return b.Where("age > ?", age)
	}
}

func TestReusableScopes(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	mustCreate(t, &User{Name: "sc-young-active", Active: true, Age: 30})
	mustCreate(t, &User{Name: "sc-old-inactive", Active: false, Age: 50})
	mustCreate(t, &User{Name: "sc-old-active", Active: true, Age: 60})

	got, err := repo.
		Where("name LIKE ?", "sc-%").
		Scopes(activeOnly, olderThan(40)).
		OrderBy("name ASC").
		Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "sc-old-active" {
		t.Errorf("Scopes(active, older>40) = %v, want [sc-old-active]", userNames(got))
	}

	// Scopes compose with the soft-delete scope and Count.
	if n, _ := repo.Scopes(activeOnly).Where("name LIKE ?", "sc-%").Count(ctx); n != 2 {
		t.Errorf("active count = %d, want 2", n)
	}
}
