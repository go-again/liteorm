package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
	"liteorm.org/query"
)

func TestQueryFindFirstExists(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	for i, n := range []string{"q-alice", "q-bob", "q-carol"} {
		mustCreate(t, &User{Name: n, Age: int64(20 + i)})
	}

	got, err := repo.Where("name LIKE ?", "q-%").OrderBy("name ASC").Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "q-alice" || got[2].Name != "q-carol" {
		t.Errorf("Find ordered = %v", userNames(got))
	}

	first, err := repo.Where("name LIKE ?", "q-%").OrderBy("name DESC").First(ctx)
	if err != nil || first.Name != "q-carol" {
		t.Errorf("First = %q err=%v, want q-carol", first.Name, err)
	}

	if _, err := repo.Where("name = ?", "q-nobody").First(ctx); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("First(missing) err = %v, want ErrNoRows", err)
	}

	if ok, _ := repo.Where("name = ?", "q-alice").Exists(ctx); !ok {
		t.Error("Exists(q-alice) = false, want true")
	}
}

func TestQueryTypedPredicatesAndPagination(t *testing.T) {
	ctx := context.Background()
	for i := range 5 {
		mustCreate(t, &User{Name: "page-user", Age: int64(i)})
	}

	// Typed predicate via the query builder on the same Session.
	young, err := query.Select[User](DB).
		Filter(query.And(
			query.Col[string]("name").Eq("page-user"),
			query.Col[int64]("age").Lt(3),
		)).
		Order(query.Asc(query.Col[int64]("age"))).
		All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(young) != 3 || young[0].Age != 0 || young[2].Age != 2 {
		t.Errorf("typed filter ages = %v, want [0 1 2]", userAges(young))
	}

	// Limit + Offset through the orm read surface.
	page, err := orm.NewRepo[User](DB).
		Where("name = ?", "page-user").OrderBy("age ASC").Limit(2).Offset(2).Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Age != 2 || page[1].Age != 3 {
		t.Errorf("paginated ages = %v, want [2 3]", userAges(page))
	}
}

func userNames(us []User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

func userAges(us []User) []int64 {
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = u.Age
	}
	return out
}
