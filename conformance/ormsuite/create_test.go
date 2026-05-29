package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/orm"
	"liteorm.org/query"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	u := GetUser("create-basic", Config{})
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("Create did not set the primary key")
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Errorf("auto timestamps not stamped: created=%v updated=%v", u.CreatedAt, u.UpdatedAt)
	}

	got, err := repo.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	CheckUser(t, &got, u)
}

func TestCreateInBatches(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	users := []*User{
		{Name: "batch-1", Age: 1},
		{Name: "batch-2", Age: 2},
		{Name: "batch-3", Age: 3},
	}
	if err := repo.CreateInBatches(ctx, users, 2); err != nil {
		t.Fatalf("CreateInBatches: %v", err)
	}
	// Generated keys come back only on RETURNING/OUTPUT dialects (not MySQL).
	if feat := DB.Dialect().Features(); feat.Has(dialect.FeatReturning) || feat.Has(dialect.FeatOutput) {
		for i, u := range users {
			if u.ID == 0 {
				t.Errorf("batch row %d: PK not captured", i)
			}
		}
	}
	n, err := repo.Where("name LIKE ?", "batch-%").Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("batch count = %d, want 3", n)
	}
}

func TestSaveUpsertsByIdentity(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	u := &User{Name: "save-me", Age: 10}
	if err := repo.Save(ctx, u); err != nil { // zero PK → insert
		t.Fatal(err)
	}
	id := u.ID
	u.Age = 11
	if err := repo.Save(ctx, u); err != nil { // non-zero PK → update
		t.Fatal(err)
	}
	if u.ID != id {
		t.Errorf("Save changed the id: %d -> %d", id, u.ID)
	}
	got, _ := repo.Get(ctx, id)
	if got.Age != 11 {
		t.Errorf("Save(update) age = %d, want 11", got.Age)
	}
	if n, _ := repo.Where("name = ?", "save-me").Count(ctx); n != 1 {
		t.Errorf("Save must not insert a duplicate, have %d", n)
	}
}

func TestFirstOrCreate(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	u := &User{Name: "foc", Age: 5}
	created, err := repo.FirstOrCreate(ctx, u, query.Col[string]("name").Eq("foc"))
	if err != nil || !created {
		t.Fatalf("first call should create: created=%v err=%v", created, err)
	}
	again := &User{Name: "foc", Age: 999}
	created, err = repo.FirstOrCreate(ctx, again, query.Col[string]("name").Eq("foc"))
	if err != nil {
		t.Fatal(err)
	}
	if created || again.ID != u.ID || again.Age != 5 {
		t.Errorf("second call should load the existing row, got created=%v %+v", created, again)
	}
}
