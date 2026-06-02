package ormsuite

import (
	"context"
	"errors"
	"testing"

	"liteorm.org/orm"
)

// TestFindInBatches walks the matching rows in fixed-size chunks, in primary-key
// order, covering every row exactly once (gorm/xorm batched scan).
func TestFindInBatches(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	var want []int64
	for range 7 {
		u := &User{Name: "fib"}
		mustCreate(t, u)
		want = append(want, u.ID)
	}

	var got []int64
	var batchSizes []int
	err := repo.Where("name = ?", "fib").FindInBatches(ctx, 3, func(batch []User) error {
		batchSizes = append(batchSizes, len(batch))
		for _, u := range batch {
			got = append(got, u.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 7 rows in chunks of 3 → 3, 3, 1.
	if len(batchSizes) != 3 || batchSizes[0] != 3 || batchSizes[1] != 3 || batchSizes[2] != 1 {
		t.Errorf("batch sizes = %v, want [3 3 1]", batchSizes)
	}
	if len(got) != 7 {
		t.Fatalf("covered %d rows, want 7", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("batch order wrong at %d: got %d want %d", i, got[i], want[i])
		}
	}
}

// TestFindInBatchesAbortsOnError stops and propagates when the callback errors.
func TestFindInBatchesAbortsOnError(t *testing.T) {
	ctx := context.Background()
	repo := orm.NewRepo[User](DB)
	for range 5 {
		mustCreate(t, &User{Name: "fib-err"})
	}
	sentinel := errors.New("stop")
	seen := 0
	err := repo.Where("name = ?", "fib-err").FindInBatches(ctx, 2, func(batch []User) error {
		seen++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
	if seen != 1 {
		t.Errorf("callback ran %d times, want 1 (aborted after first batch)", seen)
	}
}

// TestFindInBatchesSinglePKGuard rejects a composite-PK model (keyset needs one
// ordered key).
func TestFindInBatchesSinglePKGuard(t *testing.T) {
	ctx := context.Background()
	err := orm.NewRepo[Membership](DB).FindInBatches(ctx, 10, func([]Membership) error { return nil })
	if err == nil {
		t.Error("FindInBatches on a composite-PK model should error")
	}
}
