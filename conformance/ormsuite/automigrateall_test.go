package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

// TestAutoMigrateAll covers the variadic one-liner: it migrates several models in
// one call (the suite's own TestMain already relies on it), and rejects a
// non-struct argument with a clear error.
func TestAutoMigrateAll(t *testing.T) {
	ctx := context.Background()

	// Re-running over the already-migrated suite models is an additive no-op.
	if err := orm.AutoMigrateAll(ctx, DB, Company{}, User{}, Doc{}); err != nil {
		t.Fatalf("AutoMigrateAll(existing) should be a no-op: %v", err)
	}
	// A pointer model is accepted too (zero value as a type carrier).
	if err := orm.AutoMigrateAll(ctx, DB, &Event{}); err != nil {
		t.Fatalf("AutoMigrateAll(pointer) failed: %v", err)
	}
	// A non-struct argument is a hard error, not a silent skip.
	if err := orm.AutoMigrateAll(ctx, DB, 42); err == nil {
		t.Error("AutoMigrateAll with a non-struct argument should error")
	}
}
