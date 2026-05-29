package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/query"
)

// TestCursorPagination walks the whole result set in keyset ("cursor") pages —
// WHERE id > <last seen> ORDER BY id LIMIT n — the stable, offset-free pagination
// pattern bun showcases for APIs.
func TestCursorPagination(t *testing.T) {
	ctx := context.Background()
	var ids []int64
	for range 5 {
		u := &User{Name: "page"}
		mustCreate(t, u)
		ids = append(ids, u.ID)
	}
	id := query.Col[int64]("id")
	page := func(after int64) []User {
		out, err := query.Select[User](DB).
			Where("name = ?", "page").
			Filter(id.Gt(after)).
			Order(query.Asc(id)).
			Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	var seen []int64
	var pages int
	for cursor := int64(0); ; {
		batch := page(cursor)
		if len(batch) == 0 {
			break
		}
		pages++
		for _, u := range batch {
			seen = append(seen, u.ID)
		}
		cursor = batch[len(batch)-1].ID
	}
	if pages != 3 { // 2 + 2 + 1
		t.Errorf("walked %d pages, want 3", pages)
	}
	if len(seen) != 5 {
		t.Fatalf("cursor walk saw %d rows, want 5", len(seen))
	}
	for i := range ids {
		if seen[i] != ids[i] {
			t.Errorf("cursor order wrong at %d: got %d want %d", i, seen[i], ids[i])
		}
	}
}

// TestOffsetPagination covers classic LIMIT/OFFSET paging (portable to MSSQL's
// OFFSET…FETCH via the ordered query).
func TestOffsetPagination(t *testing.T) {
	ctx := context.Background()
	var ids []int64
	for range 5 {
		u := &User{Name: "offpage"}
		mustCreate(t, u)
		ids = append(ids, u.ID)
	}
	id := query.Col[int64]("id")
	page2, err := query.Select[User](DB).
		Where("name = ?", "offpage").
		Order(query.Asc(id)).
		Limit(2).Offset(2).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID != ids[2] || page2[1].ID != ids[3] {
		t.Errorf("offset page = %v, want ids[2:4]=%v", idsOfUsers(page2), ids[2:4])
	}
}

func idsOfUsers(us []User) []int64 {
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = u.ID
	}
	return out
}
