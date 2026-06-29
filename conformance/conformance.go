// Package conformance is liteorm's cross-dialect conformance suite: one set of
// behavior-level scenarios run against every backend, so adding a backend is
// "implement the contracts and go green". The same scenarios run live on SQLite
// and (when LITEORM_PG_DSN is set) on Postgres.
package conformance

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/gen"
	"liteorm.org/migrate"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// User is the shared model exercised by every scenario.
type User struct {
	ID        int64
	Name      string
	Email     string
	CreatedAt time.Time
}

func (User) TableName() string { return "users" }

// Backend describes a database under test: how to open a fresh handle, the
// dialect-specific CREATE TABLE, and an optional reset run before it.
type Backend struct {
	Name   string
	Open   func() (*liteorm.DB, error)
	Schema string
	Reset  string // e.g. "DROP TABLE IF EXISTS users" for a persistent server
}

func (b Backend) setup(t *testing.T) *liteorm.DB {
	t.Helper()
	db, err := b.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if b.Reset != "" {
		if _, err := db.ExecContext(ctx, b.Reset); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, b.Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// Run executes every scenario against b.
func Run(t *testing.T, b Backend) {
	t.Run("CRUDAndReturning", func(t *testing.T) { crudAndReturning(t, b) })
	t.Run("QueryAllAndIter", func(t *testing.T) { queryAllAndIter(t, b) })
	t.Run("UpsertAndUniqueViolation", func(t *testing.T) { upsertAndUniqueViolation(t, b) })
	t.Run("NestedTxSavepointRollback", func(t *testing.T) { nestedTxSavepointRollback(t, b) })
	t.Run("RawEscapeHatch", func(t *testing.T) { rawEscapeHatch(t, b) })
	t.Run("QueryFinishersAndBulk", func(t *testing.T) { queryFinishersAndBulk(t, b) })
	t.Run("MigrateRunner", func(t *testing.T) { migrateRunner(t, b) })
	t.Run("CodegenFromDB", func(t *testing.T) { codegenFromDB(t, b) })
	t.Run("FieldCodecRoundTrip", func(t *testing.T) { codecRoundTrip(t, b) })
}

// cfDoc exercises field codecs across dialects: a JSON (TEXT) column and a gob
// (BLOB) column, both round-tripped through AutoMigrate and the repository.
type cfDoc struct {
	ID   int64          `orm:"id,pk"`
	Meta map[string]any `orm:"meta,codec:json"`
	Tags []string       `orm:"tags,codec:gob"`
}

func (cfDoc) TableName() string { return "cf_docs" }

func codecRoundTrip(t *testing.T, b Backend) {
	ctx := context.Background()
	db, err := b.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS cf_docs"); err != nil {
		t.Fatal(err)
	}
	if err := orm.AutoMigrate[cfDoc](ctx, db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := orm.NewRepo[cfDoc](db)
	d := &cfDoc{Meta: map[string]any{"k": "v"}, Tags: []string{"a", "b"}}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create: %v", err)
	}
	// orm read decodes both codecs.
	got, err := repo.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta["k"] != "v" || len(got.Tags) != 2 || got.Tags[0] != "a" {
		t.Fatalf("orm codec round-trip on %s = %+v", b.Name, got)
	}
	// query front-end read decodes them the same way (uniform across front-ends).
	q, err := query.Select[cfDoc](db).Where("id = ?", d.ID).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q.Meta["k"] != "v" || len(q.Tags) != 2 {
		t.Fatalf("query codec round-trip on %s = %+v", b.Name, q)
	}
}

func codegenFromDB(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t) // creates the users table

	models, err := gen.FromDB(ctx, db, "users")
	if err != nil {
		t.Fatalf("FromDB: %v", err)
	}
	if len(models) != 1 || models[0].Table != "users" {
		t.Fatalf("models = %+v", models)
	}
	cols := map[string]bool{}
	for _, f := range models[0].Fields {
		cols[f.Column] = true
	}
	for _, want := range []string{"id", "name", "email", "created_at"} {
		if !cols[want] {
			t.Errorf("introspected columns missing %q (have %+v)", want, models[0].Fields)
		}
	}

	var buf bytes.Buffer
	if err := gen.WriteModels(&buf, "models", models...); err != nil { // also validates valid Go
		t.Fatalf("WriteModels: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "func (Users) TableName()") || !strings.Contains(out, "UsersColumns") {
		t.Errorf("generated code unexpected:\n%s", out)
	}
}

func migrateRunner(t *testing.T, b Backend) {
	ctx := context.Background()
	db, err := b.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, tbl := range []string{"migdemo", "schema_migrations"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Fatal(err)
		}
	}

	migs := []migrate.Migration{
		{Version: 1, Name: "create", Up: "CREATE TABLE migdemo (id BIGINT NOT NULL)", Down: "DROP TABLE migdemo"},
		{Version: 2, Name: "seed", Up: "INSERT INTO migdemo (id) VALUES (1)", Down: "DELETE FROM migdemo"},
	}
	m := migrate.New(db)

	if n, err := m.Up(ctx, migs); err != nil || n != 2 {
		t.Fatalf("Up = %d, %v; want 2", n, err)
	}
	if v, dirty, _ := m.Version(ctx); v != 2 || dirty {
		t.Errorf("Version = %d dirty=%v; want 2,false", v, dirty)
	}
	if n, err := m.Up(ctx, migs); err != nil || n != 0 { // idempotent
		t.Errorf("re-Up = %d, %v; want 0", n, err)
	}
	if c := migCount(t, db); c != 1 {
		t.Errorf("after Up, migdemo rows = %d; want 1", c)
	}

	if err := m.Down(ctx, migs); err != nil { // rolls back the seed
		t.Fatal(err)
	}
	if v, _, _ := m.Version(ctx); v != 1 {
		t.Errorf("after Down, Version = %d; want 1", v)
	}
	if st, err := m.Status(ctx, migs); err != nil || !st[0].Applied || st[1].Applied {
		t.Errorf("Status = %+v, %v", st, err)
	}
	if c := migCount(t, db); c != 0 {
		t.Errorf("after Down, migdemo rows = %d; want 0", c)
	}

	if err := m.DownTo(ctx, migs, 0); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := m.Version(ctx); v != 0 {
		t.Errorf("after DownTo(0), Version = %d; want 0", v)
	}
	if cols, _ := orm.IntrospectColumns(ctx, db, "migdemo"); len(cols) != 0 {
		t.Errorf("migdemo should be dropped, has %d columns", len(cols))
	}
}

func migCount(t *testing.T, db *liteorm.DB) int64 {
	t.Helper()
	type cnt struct {
		Total int64 `db:"total"`
	}
	out, err := query.Raw[cnt](context.Background(), db, "SELECT count(*) AS total FROM migdemo")
	if err != nil || len(out) != 1 {
		t.Fatalf("count: %v", err)
	}
	return out[0].Total
}

func queryFinishersAndBulk(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)
	repo := query.NewRepo[User](db)

	// InsertMany: native CopyFrom on Postgres, multi-row VALUES elsewhere.
	batch := []User{
		{Name: "a", Email: "a@x.io", CreatedAt: now()},
		{Name: "b", Email: "b@x.io", CreatedAt: now()},
		{Name: "c", Email: "c@x.io", CreatedAt: now()},
	}
	if err := repo.InsertMany(ctx, batch); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	if n, err := query.Select[User](db).Count(ctx); err != nil || n != 3 {
		t.Errorf("Count = %d, %v; want 3", n, err)
	}
	if ex, err := query.Select[User](db).Filter(query.Col[string]("email").Eq("a@x.io")).Exists(ctx); err != nil || !ex {
		t.Errorf("Exists(a) = %v, %v; want true", ex, err)
	}
	if ex, _ := query.Select[User](db).Filter(query.Col[string]("email").Eq("zzz@x.io")).Exists(ctx); ex {
		t.Error("Exists(zzz) = true, want false")
	}

	got, err := query.Select[User](db).Filter(query.Col[string]("name").Eq("b")).First(ctx)
	if err != nil || got.Name != "b" {
		t.Errorf("First(name=b) = %+v, %v", got, err)
	}
	if _, err := query.Select[User](db).Filter(query.Col[string]("name").Eq("nope")).First(ctx); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("First(name=nope) err = %v, want ErrNoRows", err)
	}

	found, err := repo.Find(ctx, query.Col[string]("name").In("a", "c"))
	if err != nil || len(found) != 2 {
		t.Errorf("Find(name in a,c) = %d rows, %v; want 2", len(found), err)
	}
}

func crudAndReturning(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)
	repo := query.NewRepo[User](db)

	u := User{Name: "alice", Email: "alice@x.io", CreatedAt: now()}
	if err := repo.Insert(ctx, &u); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("Insert did not set ID via RETURNING")
	}

	got, err := repo.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "alice" || got.Email != "alice@x.io" {
		t.Errorf("get = %+v", got)
	}

	got.Name = "alice2"
	if err := repo.Update(ctx, &got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if reread, _ := repo.Get(ctx, u.ID); reread.Name != "alice2" {
		t.Errorf("update not persisted: %+v", reread)
	}

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, u.ID); !errors.Is(err, liteorm.ErrNoRows) {
		t.Errorf("Get after delete: got %v, want ErrNoRows", err)
	}
}

func queryAllAndIter(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)
	repo := query.NewRepo[User](db)
	for _, n := range []string{"a", "b", "c"} {
		u := User{Name: n, Email: n + "@x.io", CreatedAt: now()}
		if err := repo.Insert(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}

	all, err := query.Select[User](db).Where("name <> ?", "b").OrderBy("name DESC").Limit(10).All(ctx)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 2 || all[0].Name != "c" || all[1].Name != "a" {
		t.Errorf("all = %v", names(all))
	}

	var seen []string
	for u, err := range query.Select[User](db).OrderBy("name").Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, u.Name)
		break // early stop
	}
	if len(seen) != 1 || seen[0] != "a" {
		t.Errorf("iter early-stop = %v", seen)
	}
}

func upsertAndUniqueViolation(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)
	repo := query.NewRepo[User](db)

	u := User{Name: "alice", Email: "alice@x.io", CreatedAt: now()}
	if err := repo.Insert(ctx, &u); err != nil {
		t.Fatal(err)
	}

	dup := User{Name: "other", Email: "alice@x.io", CreatedAt: now()}
	if err := repo.Insert(ctx, &dup); !errors.Is(err, liteorm.ErrUniqueViolation) {
		t.Fatalf("duplicate insert: got %v, want ErrUniqueViolation", err)
	}

	up := User{Name: "alice-updated", Email: "alice@x.io", CreatedAt: now()}
	if err := repo.Upsert(ctx, &up, query.OnConflict("email").DoUpdate("name")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _ := repo.Get(ctx, u.ID); got.Name != "alice-updated" {
		t.Errorf("upsert did not update name: %+v", got)
	}
}

func nestedTxSavepointRollback(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := query.NewRepo[User](tx).Insert(ctx, &User{Name: "keep", Email: "keep@x.io", CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	sp, err := tx.Begin(ctx) // nested = savepoint
	if err != nil {
		t.Fatal(err)
	}
	if err := query.NewRepo[User](sp).Insert(ctx, &User{Name: "drop", Email: "drop@x.io", CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	all, err := query.Select[User](db).OrderBy("name").All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "keep" {
		t.Errorf("after savepoint rollback, users = %v, want [keep]", names(all))
	}
}

func rawEscapeHatch(t *testing.T, b Backend) {
	ctx := context.Background()
	db := b.setup(t)
	repo := query.NewRepo[User](db)
	for _, n := range []string{"a", "b"} {
		if err := repo.Insert(ctx, &User{Name: n, Email: n + "@x.io", CreatedAt: now()}); err != nil {
			t.Fatal(err)
		}
	}
	type stat struct {
		Total int64 `db:"total"`
	}
	// Raw passes SQL verbatim (the escape hatch owns its dialect), so keep this
	// portable — no bound placeholder, which would differ per dialect.
	out, err := query.Raw[stat](ctx, db, `SELECT count(*) AS total FROM users`)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len(out) != 1 || out[0].Total != 2 {
		t.Errorf("raw count = %v, want [{2}]", out)
	}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func names(us []User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}
