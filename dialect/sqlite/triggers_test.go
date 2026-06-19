package sqlite_test

import (
	"context"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

func mustExec(t *testing.T, db *liteorm.DB, sql string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func scalarInt(t *testing.T, db *liteorm.DB, sql string, args ...any) int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDropTriggers_Multiple_AllSucceed(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	mustExec(t, db, "CREATE TABLE items (id INTEGER PRIMARY KEY)")
	mustExec(t, db, "CREATE TRIGGER t1 AFTER INSERT ON items BEGIN SELECT 1; END")
	mustExec(t, db, "CREATE TRIGGER t2 AFTER UPDATE ON items BEGIN SELECT 1; END")

	if err := sqlite.DropTriggers(ctx, db, "t1", "t2", "never_existed"); err != nil {
		t.Fatalf("DropTriggers: %v", err)
	}
	if n := scalarInt(t, db, "SELECT count(*) FROM sqlite_master WHERE type='trigger'"); n != 0 {
		t.Errorf("trigger count = %d, want 0", n)
	}
}

func TestDropTriggers_AllMissing_IsNoOp(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := sqlite.DropTriggers(ctx, db, "a", "b", "c"); err != nil {
		t.Fatalf("DropTriggers on missing names: %v", err)
	}
}

// A name that isn't a bare identifier but is a legal quoted identifier must still
// drop correctly — proving the helper quotes rather than rejecting on a regex.
func TestDropTriggers_QuotableName_Drops(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	mustExec(t, db, "CREATE TABLE items (id INTEGER PRIMARY KEY)")
	mustExec(t, db, `CREATE TRIGGER "legacy-ai" AFTER INSERT ON items BEGIN SELECT 1; END`)

	if err := sqlite.DropTriggers(ctx, db, "legacy-ai"); err != nil {
		t.Fatalf("DropTriggers quotable name: %v", err)
	}
	if n := scalarInt(t, db, "SELECT count(*) FROM sqlite_master WHERE type='trigger'"); n != 0 {
		t.Errorf("trigger count = %d, want 0 (quoted name should drop)", n)
	}
}

func TestDropTriggers_EmptyName_Errors(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	mustExec(t, db, "CREATE TABLE items (id INTEGER PRIMARY KEY)")
	mustExec(t, db, "CREATE TRIGGER good AFTER INSERT ON items BEGIN SELECT 1; END")

	// The empty name short-circuits before "good" is attempted.
	if err := sqlite.DropTriggers(ctx, db, "  ", "good"); err == nil {
		t.Fatal("DropTriggers with an empty name should error")
	}
	if n := scalarInt(t, db, "SELECT count(*) FROM sqlite_master WHERE name='good'"); n != 1 {
		t.Errorf("'good' count = %d, want 1 (short-circuit before it)", n)
	}
}
