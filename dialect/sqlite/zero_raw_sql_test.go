package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// End-to-end coverage for the typed APIs that retire raw fragments (the pantry
// "zero raw SQL" filing): OnConflict.DoNothing, Update.Inc, PluckExpr,
// ExistsField, and query.Match against a real FTS5 table.

func openDB(t *testing.T) *liteorm.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "zrs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type vocab struct {
	Word string `orm:"word,pk"`
}

func (vocab) TableName() string { return "vocab" }

func TestDoNothing_SkipsExisting(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[vocab](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[vocab](db)
	for range 2 { // second insert conflicts on the word PK and is ignored
		if err := repo.Upsert(ctx, &vocab{Word: "apple"}, query.OnConflict("word").DoNothing()); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	n, err := query.Select[vocab](db).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1 (DoNothing must skip the duplicate)", n)
	}
}

type counter struct {
	ID int64 `orm:"id,pk"`
	N  int64
}

func (counter) TableName() string { return "counters" }

func TestUpdateInc_InDatabase(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[counter](ctx, db); err != nil {
		t.Fatal(err)
	}
	c := &counter{N: 5}
	if err := orm.NewRepo[counter](db).Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := query.Update[counter](db).Inc("n", 1).Where("id = ?", c.ID).Exec(ctx); err != nil {
			t.Fatalf("inc: %v", err)
		}
	}
	got, err := orm.NewRepo[counter](db).Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 7 {
		t.Errorf("N = %d, want 7 (5 + 1 + 1, incremented in the database)", got.N)
	}

	max, err := query.PluckExprFirst[counter, int64](ctx, query.Select[counter](db), "MAX(n)")
	if err != nil {
		t.Fatal(err)
	}
	if max != 7 {
		t.Errorf("MAX(n) = %d, want 7", max)
	}
}

type epost struct {
	ID    int64 `orm:"id,pk"`
	Title string
}

func (epost) TableName() string { return "eposts" }

type ecomment struct {
	ID     int64 `orm:"id,pk"`
	PostID int64
}

func (ecomment) TableName() string { return "ecomments" }

func TestExistsField_CorrelatedProjection(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[epost](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.AutoMigrate[ecomment](ctx, db); err != nil {
		t.Fatal(err)
	}
	pr := orm.NewRepo[epost](db)
	p1, p2 := &epost{Title: "has one"}, &epost{Title: "lonely"}
	_ = pr.Create(ctx, p1)
	_ = pr.Create(ctx, p2)
	_ = orm.NewRepo[ecomment](db).Create(ctx, &ecomment{PostID: p1.ID})

	hasComment := query.ExistsField("has_comment",
		query.Select[ecomment](db).Filter(
			query.Col[int64]("post_id").EqCol(query.Col[int64]("id").Of("eposts"))))

	type row struct {
		ID         int64
		Title      string
		HasComment bool
	}
	rows, err := query.Into[epost, row](ctx, query.Select[epost](db).OrderBy("id"),
		query.Name("id"), query.Name("title"), hasComment)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[0].HasComment || rows[1].HasComment {
		t.Errorf("rows = %+v, want [{1 has one true} {2 lonely false}]", rows)
	}
}

// ritem has a non-integer PK, so "rowid" is a genuinely separate implicit column
// (on an INTEGER PRIMARY KEY table SQLite makes rowid an alias of the PK column,
// and SELECT "rowid" reports the PK's name instead) — matching the real use case
// for sqlite.Rowid()/RowidCol().
type ritem struct {
	ID    string `orm:"id,pk"`
	Title string
}

func (ritem) TableName() string { return "ritems" }

func TestRowid_TypedProjectionAndOrder(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if err := orm.AutoMigrate[ritem](ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := orm.NewRepo[ritem](db)
	for _, r := range []ritem{{"a", "alpha"}, {"b", "bravo"}, {"c", "charlie"}} {
		if err := repo.Create(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}

	// RowidCol drives a typed ORDER BY and a typed Pluck — no raw "rowid" string.
	ids, err := query.Pluck[ritem, int64](ctx,
		query.Select[ritem](db).Order(query.Asc(sqlite.RowidCol())), sqlite.RowidCol())
	if err != nil {
		t.Fatalf("pluck rowid: %v", err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Errorf("rowids = %v, want [1 2 3]", ids)
	}

	// Rowid() projects the implicit column alongside a model column in Into.
	type reindexRow struct {
		Rowid int64
		Title string
	}
	rows, err := query.Into[ritem, reindexRow](ctx,
		query.Select[ritem](db).Order(query.Asc(sqlite.RowidCol())),
		sqlite.Rowid(), query.Name("title"))
	if err != nil {
		t.Fatalf("into rowid: %v", err)
	}
	if len(rows) != 3 || rows[0].Rowid != 1 || rows[0].Title != "alpha" || rows[2].Title != "charlie" {
		t.Errorf("rows = %+v, want rowid-ordered alpha/bravo/charlie", rows)
	}

	// RowidCol also composes as a typed predicate.
	got, err := query.Select[ritem](db).Filter(sqlite.RowidCol().Eq(2)).First(ctx)
	if err != nil {
		t.Fatalf("filter rowid: %v", err)
	}
	if got.Title != "bravo" {
		t.Errorf("rowid 2 = %q, want bravo", got.Title)
	}
}

type ftsDoc struct {
	Body string
}

func (ftsDoc) TableName() string { return "docs_fts" }

func TestMatch_AgainstFTS5(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE docs_fts USING fts5(body)"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"the quick brown fox", "a lazy dog sleeps"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO docs_fts(body) VALUES (?)", body); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := query.Select[ftsDoc](db).Filter(query.Match("body", "fox")).All(ctx)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(hits) != 1 || hits[0].Body != "the quick brown fox" {
		t.Errorf("match 'fox' = %+v, want the fox row", hits)
	}
}
