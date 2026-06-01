package conformance_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/migrate"
)

// TestMigrateRunnerLifecycle exercises the runner methods the suite previously
// left untested: Up/UpTo/Down/DownTo/Version/Status and the dirty-ledger refusal.
func TestMigrateRunnerLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "mig.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migs := []migrate.Migration{
		{Version: 1, Name: "t1", Up: "CREATE TABLE t1 (id INTEGER PRIMARY KEY)", Down: "DROP TABLE t1"},
		{Version: 2, Name: "t2", Up: "CREATE TABLE t2 (id INTEGER PRIMARY KEY)", Down: "DROP TABLE t2"},
		{Version: 3, Name: "t3", Up: "CREATE TABLE t3 (id INTEGER PRIMARY KEY)", Down: "DROP TABLE t3"},
	}
	m := migrate.New(db)

	mustVersion := func(want uint64) {
		t.Helper()
		v, dirty, err := m.Version(ctx)
		if err != nil || v != want || dirty {
			t.Fatalf("Version = %d dirty=%v err=%v, want %d clean", v, dirty, err, want)
		}
	}

	if n, err := m.Up(ctx, migs); err != nil || n != 3 {
		t.Fatalf("Up applied %d (err %v), want 3", n, err)
	}
	mustVersion(3)

	st, err := m.Status(ctx, migs)
	if err != nil || len(st) != 3 || !st[2].Applied {
		t.Fatalf("Status = %+v err=%v", st, err)
	}

	if err := m.Down(ctx, migs); err != nil { // one step: 3 -> 2
		t.Fatal(err)
	}
	mustVersion(2)

	if err := m.DownTo(ctx, migs, 0); err != nil { // all the way down
		t.Fatal(err)
	}
	mustVersion(0)

	if n, err := m.UpTo(ctx, migs, 2); err != nil || n != 2 { // partial up
		t.Fatalf("UpTo applied %d (err %v), want 2", n, err)
	}
	mustVersion(2)
}

func TestMigrateRunnerDirty(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "dirty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bad := []migrate.Migration{
		{Version: 1, Name: "ok", Up: "CREATE TABLE ok (id INTEGER PRIMARY KEY)"},
		{Version: 2, Name: "broken", Up: "THIS IS NOT SQL"},
	}
	m := migrate.New(db)
	if _, err := m.Up(ctx, bad); err == nil {
		t.Fatal("a broken migration should fail Up")
	}
	// The ledger is now dirty; a subsequent Up must refuse with DirtyError.
	_, err = m.Up(ctx, bad)
	var de *migrate.DirtyError
	if !errors.As(err, &de) {
		t.Fatalf("after a failed migration, Up should return *DirtyError, got %v", err)
	}
	// Force clears the dirty flag to a known version.
	if err := m.Force(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, dirty, _ := m.Version(ctx); dirty {
		t.Error("Force must clear the dirty flag")
	}
}
