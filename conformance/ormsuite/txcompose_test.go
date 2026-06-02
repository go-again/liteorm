package ormsuite

import (
	"context"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
)

// inviteTeam is a repository function written against liteorm.Session, so the
// exact same code composes onto a *DB or a transaction — bun's IDB-interface
// pattern, but liteorm's Session interface needs no wrapper.
func inviteTeam(ctx context.Context, sess liteorm.Session, names ...string) error {
	repo := orm.NewRepo[User](sess)
	for _, n := range names {
		if err := repo.Create(ctx, &User{Name: n}); err != nil {
			return err
		}
	}
	return nil
}

func TestTransactionComposition(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)

	// (1) run directly on the DB
	if err := inviteTeam(ctx, DB, "txc-direct-1", "txc-direct-2"); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Where("name LIKE ?", "txc-direct-%").Count(ctx); n != 2 {
		t.Errorf("direct run count = %d, want 2", n)
	}

	// (2) compose the same function inside a committed transaction
	tx, err := DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inviteTeam(ctx, tx, "txc-tx-1", "txc-tx-2"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Where("name LIKE ?", "txc-tx-%").Count(ctx); n != 2 {
		t.Errorf("committed tx count = %d, want 2", n)
	}

	// (3) compose it in a rolled-back transaction — its writes vanish, but were
	// visible inside the transaction before rollback.
	tx2, err := DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inviteTeam(ctx, tx2, "txc-rb-1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := orm.NewRepo[User](tx2).Where("name = ?", "txc-rb-1").Count(ctx); n != 1 {
		t.Errorf("write should be visible inside its own tx, count = %d", n)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Where("name = ?", "txc-rb-1").Count(ctx); n != 0 {
		t.Errorf("rolled-back write should be gone, count = %d", n)
	}
}
