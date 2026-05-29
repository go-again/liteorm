package conformance_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"liteorm.org/dialect/postgres"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

// TestSetOpsLiveSQLite exercises INTERSECT / EXCEPT against a live SQLite database.
func TestSetOpsLiveSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "so.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`INSERT INTO people (id,name,age) VALUES (1,'Ada',12),(2,'Bo',40),(3,'Cy',70),(4,'Di',55)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	young := func() *query.SelectBuilder[Person] { // age < 50: Ada, Bo
		return query.Select[Person](db).Filter(query.Col[int64]("age").Lt(50))
	}
	adults := func() *query.SelectBuilder[Person] { // age > 30: Bo, Cy, Di
		return query.Select[Person](db).Filter(query.Col[int64]("age").Gt(30))
	}

	inter, err := young().Intersect(adults()).OrderBy("id").All(ctx) // young AND adult → Bo
	if err != nil {
		t.Fatal(err)
	}
	if len(inter) != 1 || inter[0].Name != "Bo" {
		t.Errorf("INTERSECT = %v, want [Bo]", idsOf(inter))
	}
	exc, err := young().Except(adults()).OrderBy("id").All(ctx) // young NOT adult → Ada
	if err != nil {
		t.Fatal(err)
	}
	if len(exc) != 1 || exc[0].Name != "Ada" {
		t.Errorf("EXCEPT = %v, want [Ada]", idsOf(exc))
	}
}

type Event struct {
	ID   int64
	Kind string
	Seq  int64
}

func (Event) TableName() string { return "events" }

// TestLockingAndDistinctOnLivePG exercises FOR UPDATE and DISTINCT ON against a
// live Postgres (the dialects that support them). Skipped without LITEORM_PG_DSN.
func TestLockingAndDistinctOnLivePG(t *testing.T) {
	dsn := os.Getenv("LITEORM_PG_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_PG_DSN to run the Postgres locking / DISTINCT ON test")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS events`,
		`CREATE TABLE events (id BIGINT PRIMARY KEY, kind TEXT NOT NULL, seq BIGINT NOT NULL)`,
		`INSERT INTO events (id,kind,seq) VALUES (1,'a',1),(2,'a',3),(3,'b',2),(4,'b',5)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	// DISTINCT ON (kind) ORDER BY kind, seq DESC → the highest-seq row per kind.
	latest, err := query.Select[Event](db).
		DistinctOn(query.Col[string]("kind").Field()).
		Order(query.Asc(query.Col[string]("kind")), query.Desc(query.Col[int64]("seq"))).
		All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 || latest[0].Seq != 3 || latest[1].Seq != 5 {
		t.Errorf("DISTINCT ON latest-per-kind = %+v, want seq 3 then 5", latest)
	}
	// Count over a DISTINCT ON query counts distinct groups (as a derived table),
	// not count(*) over the DISTINCT ON projection.
	if n, err := query.Select[Event](db).DistinctOn(query.Col[string]("kind").Field()).Count(ctx); err != nil || n != 2 {
		t.Errorf("Count(DistinctOn kind) = %d err=%v, want 2", n, err)
	}

	// FOR UPDATE locks the matched rows; it must execute inside a transaction.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := query.Select[Event](tx).
		Filter(query.Col[string]("kind").Eq("a")).
		ForUpdate().SkipLocked().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != 2 {
		t.Errorf("FOR UPDATE SKIP LOCKED returned %d rows, want 2", len(locked))
	}
}
