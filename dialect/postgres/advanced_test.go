package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/dialect/postgres"
	"liteorm.org/query"
)

// pgDoc maps the jsonb + text[] table the operator tests query.
type pgDoc struct {
	ID   int64
	Data string   // jsonb, scanned back as its text
	Tags []string // text[]
}

func (pgDoc) TableName() string { return "pgdocs" }

func openPG(t *testing.T) *liteorm.DB {
	t.Helper()
	dsn := os.Getenv("LITEORM_PG_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_PG_DSN to run the Postgres advanced tests")
	}
	db, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestListenNotify(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)

	l, err := postgres.Listen(ctx, db, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close(ctx)

	if err := postgres.Notify(ctx, db, "jobs", "build-42"); err != nil {
		t.Fatal(err)
	}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := l.Receive(rctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if n.Channel != "jobs" || n.Payload != "build-42" {
		t.Fatalf("notification = %+v, want channel=jobs payload=build-42", n)
	}
	if n.PID == 0 {
		t.Errorf("notification PID should be set")
	}
}

func TestJSONBAndArrayOperatorsLive(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)

	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS pgdocs`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE pgdocs (
		id   BIGINT PRIMARY KEY,
		data JSONB NOT NULL,
		tags TEXT[] NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id   int64
		data string
		tags []string
	}{
		{1, `{"city":"Paris","active":true}`, []string{"go", "db"}},
		{2, `{"city":"Berlin","active":false}`, []string{"rust", "db"}},
		{3, `{"city":"Paris","active":false}`, []string{"go", "web"}},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO pgdocs (id, data, tags) VALUES ($1, $2, $3)`, r.id, r.data, r.tags); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(ds []pgDoc) []int64 {
		out := make([]int64, len(ds))
		for i, d := range ds {
			out[i] = d.ID
		}
		return out
	}

	t.Run("json path ->> equality", func(t *testing.T) {
		got, err := query.Select[pgDoc](db).
			Filter(query.JSON("data").Key("city").Eq("Paris")).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 2 || g[0] != 1 || g[1] != 3 {
			t.Fatalf("city=Paris → %v, want [1 3]", g)
		}
	})

	t.Run("jsonb containment @>", func(t *testing.T) {
		got, err := query.Select[pgDoc](db).
			Filter(query.JSON("data").Contains(map[string]any{"active": true})).
			All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 1 || g[0] != 1 {
			t.Fatalf("active=true → %v, want [1]", g)
		}
	})

	t.Run("array contains @>", func(t *testing.T) {
		got, err := query.Select[pgDoc](db).
			Filter(query.Array[string]("tags").Contains("go")).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 2 || g[0] != 1 || g[1] != 3 {
			t.Fatalf("tags @> {go} → %v, want [1 3]", g)
		}
	})

	t.Run("array overlap && and ANY", func(t *testing.T) {
		got, err := query.Select[pgDoc](db).
			Filter(query.Array[string]("tags").Overlaps("rust", "web")).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 2 || g[0] != 2 || g[1] != 3 {
			t.Fatalf("tags && {rust,web} → %v, want [2 3]", g)
		}
		got, err = query.Select[pgDoc](db).
			Filter(query.Array[string]("tags").Has("db")).
			OrderBy("id").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != 2 || g[0] != 1 || g[1] != 2 {
			t.Fatalf("'db' = ANY(tags) → %v, want [1 2]", g)
		}
	})
}
