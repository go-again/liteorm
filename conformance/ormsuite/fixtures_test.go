package ormsuite

import (
	"context"
	"errors"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/orm"
)

// seedFunc is one fixture step: it persists rows (often reading ids created by an
// earlier step) through the given session.
type seedFunc func(ctx context.Context, sess liteorm.Session) error

// seed runs the fixture steps in order inside ONE transaction, so a fixture set
// lands atomically — if any step fails the whole set is rolled back. Reference
// resolution is ordinary Go: a step closes over the structs an earlier step
// created and reads their generated ids.
func seed(ctx context.Context, db *liteorm.DB, steps ...seedFunc) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if err := step(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}
	return tx.Commit(ctx)
}

// TestFixturesSeedGraph seeds a company→users graph declaratively and asserts the
// foreign keys were wired from earlier steps' generated ids.
func TestFixturesSeedGraph(t *testing.T) {
	ctx := context.Background()
	var acme, globex Company

	err := seed(ctx, DB,
		func(ctx context.Context, sess liteorm.Session) error {
			acme = Company{Name: "fx-acme"}
			globex = Company{Name: "fx-globex"}
			r := orm.NewRepo[Company](sess)
			if err := r.Create(ctx, &acme); err != nil {
				return err
			}
			return r.Create(ctx, &globex)
		},
		func(ctx context.Context, sess liteorm.Session) error {
			r := orm.NewRepo[User](sess)
			for _, n := range []string{"fx-u1", "fx-u2"} {
				if err := r.Create(ctx, &User{Name: n, CompanyID: acme.ID}); err != nil {
					return err
				}
			}
			return r.Create(ctx, &User{Name: "fx-u3", CompanyID: globex.ID})
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	users := orm.NewRepo[User](DB)
	if n, _ := users.Where("company_id = ?", acme.ID).Count(ctx); n != 2 {
		t.Errorf("acme users = %d, want 2", n)
	}
	if n, _ := users.Where("company_id = ?", globex.ID).Count(ctx); n != 1 {
		t.Errorf("globex users = %d, want 1", n)
	}
}

// TestFixturesSeedIsAtomic proves a failing step rolls the whole fixture back.
func TestFixturesSeedIsAtomic(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	err := seed(ctx, DB,
		func(ctx context.Context, sess liteorm.Session) error {
			return orm.NewRepo[Company](sess).Create(ctx, &Company{Name: "fx-doomed"})
		},
		func(ctx context.Context, sess liteorm.Session) error {
			return boom // second step fails → first step must roll back
		},
	)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n, _ := orm.NewRepo[Company](DB).Where("name = ?", "fx-doomed").Count(ctx); n != 0 {
		t.Errorf("failed fixture left %d rows, want 0 (rolled back)", n)
	}
}
