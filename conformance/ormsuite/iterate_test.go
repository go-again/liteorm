package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

// TestIterateStreaming streams rows lazily with range-over-func (iter.Seq2)
// instead of materializing a slice — liteorm's answer to xorm's Iterate/Rows and
// gorm's FindInBatches, but as an idiomatic Go 1.23 range loop.
func TestIterateStreaming(t *testing.T) {
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		mustCreate(t, &User{Name: "iter", Age: int64(i)})
	}
	age := query.Col[int64]("age")
	stream := func() *query.SelectBuilder[User] {
		return query.Select[User](DB).Where("name = ?", "iter").Order(query.Asc(age))
	}

	var n, sum int64
	for u, err := range stream().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		sum += u.Age
	}
	if n != 5 || sum != 15 {
		t.Errorf("streamed n=%d sum=%d, want 5/15", n, sum)
	}

	// Breaking out mid-stream stops cleanly (the underlying rows are closed by the
	// iterator's stop path) — a subsequent query on the same DB still works.
	read := 0
	for _, err := range stream().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		read++
		if read == 2 {
			break
		}
	}
	if read != 2 {
		t.Errorf("early break read %d rows, want 2", read)
	}
	if n, _ := orm.NewRepo[User](DB).Where("name = ?", "iter").Count(ctx); n != 5 {
		t.Errorf("DB still usable after early break: count=%d, want 5", n)
	}
}
