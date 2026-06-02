package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

func exists(t *testing.T, name string) bool {
	t.Helper()
	ok, err := orm.NewRepo[User](DB).Where("name = ?", name).Exists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestTransactionCommitRollback(t *testing.T) {
	ctx := context.Background()

	tx, err := DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.NewRepo[User](tx).Create(ctx, &User{Name: "tx-commit"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(t, "tx-commit") {
		t.Error("committed row should be visible")
	}

	tx2, _ := DB.Begin(ctx)
	_ = orm.NewRepo[User](tx2).Create(ctx, &User{Name: "tx-rollback"})
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(t, "tx-rollback") {
		t.Error("rolled-back row should not be visible")
	}
}

func TestTransactionSavepointAndInterop(t *testing.T) {
	ctx := context.Background()
	tx, err := DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// orm write + query read on the same transaction (interop).
	outer := &User{Name: "tx-outer"}
	if err := orm.NewRepo[User](tx).Create(ctx, outer); err != nil {
		t.Fatal(err)
	}
	back, err := query.Select[User](tx).Filter(query.Col[int64]("id").Eq(outer.ID)).First(ctx)
	if err != nil || back.Name != "tx-outer" {
		t.Errorf("interop read on tx: %q err=%v", back.Name, err)
	}

	// Nested Begin = savepoint; rolling it back drops only the inner write.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = orm.NewRepo[User](sp).Create(ctx, &User{Name: "tx-inner"})
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if !exists(t, "tx-outer") {
		t.Error("outer write should persist after inner savepoint rollback")
	}
	if exists(t, "tx-inner") {
		t.Error("inner write should be gone after savepoint rollback")
	}
}
