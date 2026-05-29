package ormsuite

import (
	"context"
	"testing"
	"time"

	"liteorm.org/orm"
	"liteorm.org/query"
)

func TestUpdateBumpsAutoTime(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "upd-time", Age: 1}
	mustCreate(t, u)
	before, _ := repo.Get(ctx, u.ID) // the stored (column-precision) timestamp

	// MySQL's DATETIME is second-precision, so sleep across a second boundary to
	// make the bump observable there; sub-second dialects need only a moment.
	sleep := 5 * time.Millisecond
	if DB.Dialect().Name() == "mysql" {
		sleep = 1100 * time.Millisecond
	}
	time.Sleep(sleep)

	u.Age = 2
	if err := repo.Update(ctx, u); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.Get(ctx, u.ID)
	if after.Age != 2 {
		t.Errorf("update age = %d, want 2", after.Age)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("autoUpdateTime not bumped: %v !after %v", after.UpdatedAt, before.UpdatedAt)
	}
}

func TestUpdatesSelectOmit(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	u := &User{Name: "upd-cols", Age: 1, Active: true}
	mustCreate(t, u)

	// Updates writes only the named columns.
	u.Age = 9
	u.Active = false // should NOT persist (not in the column list)
	if err := repo.Updates(ctx, u, "age"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, u.ID)
	if got.Age != 9 || !got.Active {
		t.Errorf("Updates(\"age\"): age=%d active=%v, want 9/true", got.Age, got.Active)
	}

	// Omit excludes a column from a full update.
	u.Name = "renamed"
	u.Age = 100 // omitted
	if err := repo.Omit("age").Update(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, u.ID)
	if got.Name != "renamed" || got.Age != 9 {
		t.Errorf("Omit(\"age\"): name=%q age=%d, want renamed/9", got.Name, got.Age)
	}
}

func TestMultiRowUpdateBuilder(t *testing.T) {
	ctx := context.Background()
	for i := range 4 {
		mustCreate(t, &User{Name: "mass-upd", Age: int64(i)})
	}
	n, err := query.Update[User](DB).
		Set("active", true).
		Where("name = ? AND age < ?", "mass-upd", 2).
		Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("multi-row update affected %d, want 2", n)
	}
	// SetExpr (a raw expression) over the rest.
	if _, err := query.Update[User](DB).SetExpr("age", "age + ?", 10).Where("name = ?", "mass-upd").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := orm.NewRepo[User](DB).Where("name = ?", "mass-upd").OrderBy("age ASC").Find(ctx)
	if len(got) != 4 || got[0].Age != 10 {
		t.Errorf("SetExpr ages = %v, want starting at 10", userAges(got))
	}
}
