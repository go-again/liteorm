package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

// TestAtomicIncrement updates a column from its own value in SQL — the
// concurrency-safe counter pattern (gorm's gorm.Expr("age + ?"), xorm's Incr).
func TestAtomicIncrement(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "counter", Age: 0}
	mustCreate(t, u)

	n, err := query.Update[User](DB).SetExpr("age", "age + ?", 5).Where("id = ?", u.ID).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("affected = %d, want 1", n)
	}
	if got, _ := repo.Get(ctx, u.ID); got.Age != 5 {
		t.Errorf("after +5, age = %d, want 5", got.Age)
	}

	// Decrement reads the just-written value, proving it is computed in the database.
	if _, err := query.Update[User](DB).SetExpr("age", "age - ?", 2).Where("id = ?", u.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, u.ID); got.Age != 3 {
		t.Errorf("after -2, age = %d, want 3", got.Age)
	}
}

// TestBulkExpressionUpdate applies a computed update to many rows at once.
func TestBulkExpressionUpdate(t *testing.T) {
	ctx := context.Background()
	for _, age := range []int64{1, 2, 3} {
		mustCreate(t, &User{Name: "bulkexpr", Age: age})
	}
	n, err := query.Update[User](DB).
		SetExpr("age", "age * ?", 10).
		Where("name = ?", "bulkexpr").
		Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("affected = %d, want 3", n)
	}
	sum, _ := query.Sum(ctx, query.Select[User](DB).Where("name = ?", "bulkexpr"), query.Col[int64]("age"))
	if sum != 60 { // (1+2+3)*10
		t.Errorf("sum after *10 = %d, want 60", sum)
	}
}
