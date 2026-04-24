package changeset_test

import (
	"context"
	"path/filepath"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/dialect/sqlite/changeset"
	"liteorm.org/query"
)

type User struct {
	ID   int64
	Name string
}

func (User) TableName() string { return "users" }

func openUsers(t *testing.T) *liteorm.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func count(t *testing.T, db *liteorm.DB) int {
	t.Helper()
	n, err := query.NewRepo[User](db).Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(n)
}

// TestCaptureAndApply records two inserts on a source database into a changeset,
// then replays that changeset onto an independent destination — one-way
// replication of liteorm-issued writes.
func TestCaptureAndApply(t *testing.T) {
	ctx := context.Background()
	src := openUsers(t)
	dst := openUsers(t)

	cs, err := changeset.Capture(ctx, src, nil, func(ctx context.Context, s liteorm.Session) error {
		repo := query.NewRepo[User](s)
		if err := repo.Insert(ctx, &User{Name: "alice"}); err != nil {
			return err
		}
		return repo.Insert(ctx, &User{Name: "bob"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("empty changeset")
	}
	if got := count(t, src); got != 2 {
		t.Fatalf("source has %d users after capture, want 2", got)
	}
	if got := count(t, dst); got != 0 {
		t.Fatalf("destination has %d users before apply, want 0", got)
	}

	if err := changeset.Apply(ctx, dst, cs); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := count(t, dst); got != 2 {
		t.Fatalf("destination has %d users after apply, want 2", got)
	}

	// Invert + apply on the source undoes the captured inserts (an undo log).
	inv, err := changeset.Invert(ctx, src, cs)
	if err != nil {
		t.Fatal(err)
	}
	if err := changeset.Apply(ctx, src, inv); err != nil {
		t.Fatalf("apply inverse: %v", err)
	}
	if got := count(t, src); got != 0 {
		t.Fatalf("source has %d users after applying inverse, want 0", got)
	}
}

// TestApplyConflict exercises the conflict handler: replaying an insert onto a
// row that already exists conflicts; without a handler it aborts, with Replace
// it overwrites.
func TestApplyConflict(t *testing.T) {
	ctx := context.Background()
	src := openUsers(t)
	dst := openUsers(t)

	// Destination already holds id=1.
	if _, err := dst.ExecContext(ctx, `INSERT INTO users (id, name) VALUES (1, 'zoe')`); err != nil {
		t.Fatal(err)
	}
	// Source records an insert of id=1 'alice' (first row → rowid 1).
	cs, err := changeset.Capture(ctx, src, nil, func(ctx context.Context, s liteorm.Session) error {
		return query.NewRepo[User](s).Insert(ctx, &User{Name: "alice"})
	})
	if err != nil {
		t.Fatal(err)
	}

	// No handler: the PK collision aborts the apply.
	if err := changeset.Apply(ctx, dst, cs); err == nil {
		t.Fatal("apply with no conflict handler should fail on the id=1 collision")
	}

	// Replace: the conflicting destination row is overwritten.
	err = changeset.Apply(ctx, dst, cs, changeset.WithConflictHandler(
		func(changeset.ConflictType) changeset.ConflictAction { return changeset.Replace },
	))
	if err != nil {
		t.Fatalf("apply with Replace handler: %v", err)
	}
	var name string
	row := query.NewRepo[User](dst)
	u, err := row.Get(ctx, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	name = u.Name
	if name != "alice" {
		t.Fatalf("id=1 name = %q after Replace, want alice", name)
	}
}
