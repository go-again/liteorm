package sqlgen_test

import (
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

// nonTrivialSelect: filtered SELECT + JOIN + ORDER + LIMIT — rendered across all
// four dialects. This is the R1 spike deliverable: one non-trivial query proven
// correct through liteorm's own builder for every dialect, exercising placeholder
// style, identifier quoting, and LIMIT vs OFFSET/FETCH divergence.
func nonTrivialSelect() sqlgen.Select {
	return sqlgen.Select{
		Table:    "users",
		Columns:  []sqlgen.Column{{Table: "users", Name: "id"}, {Table: "users", Name: "name"}},
		Joins:    []sqlgen.Expr{{SQL: "JOIN orders ON orders.user_id = users.id"}},
		Where:    []sqlgen.Expr{{SQL: "users.active = ?", Args: []any{true}}},
		OrderBy:  []string{"users.created_at DESC"},
		Limit:    20,
		HasLimit: true,
	}
}

// TestPlaceholderOrderingAcrossClauses locks the global-counter invariant: binds
// must number in render order across WITH → projection → FROM-subquery → WHERE,
// and the args slice must align with that order.
func TestPlaceholderOrderingAcrossClauses(t *testing.T) {
	s := sqlgen.Select{
		With: []sqlgen.CTE{{
			Name:   "w",
			Select: sqlgen.Select{Table: "cte_src", Where: []sqlgen.Expr{{SQL: "c = ?", Args: []any{"A"}}}},
		}},
		ProjectionExprs: []sqlgen.Expr{{SQL: "(SELECT count(*) FROM t WHERE p = ?) AS x", Args: []any{"B"}}},
		FromSubquery:    &sqlgen.Select{Table: "from_src", Where: []sqlgen.Expr{{SQL: "f = ?", Args: []any{"C"}}}},
		FromAlias:       "d",
		Where:           []sqlgen.Expr{{SQL: "w = ?", Args: []any{"D"}}},
	}
	got, args, err := s.Build(sqlgen.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	want := `WITH "w" AS (SELECT * FROM "cte_src" WHERE c = $1) SELECT (SELECT count(*) FROM t WHERE p = $2) AS x FROM (SELECT * FROM "from_src" WHERE f = $3) AS "d" WHERE w = $4`
	if got != want {
		t.Errorf("SQL mismatch\n got: %s\nwant: %s", got, want)
	}
	if len(args) != 4 || args[0] != "A" || args[1] != "B" || args[2] != "C" || args[3] != "D" {
		t.Errorf("args = %v, want [A B C D] (render order)", args)
	}
}

func TestSelectAcrossDialects(t *testing.T) {
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `SELECT "users"."id", "users"."name" FROM "users" JOIN orders ON orders.user_id = users.id WHERE users.active = ? ORDER BY users.created_at DESC LIMIT 20`},
		{sqlgen.Postgres, `SELECT "users"."id", "users"."name" FROM "users" JOIN orders ON orders.user_id = users.id WHERE users.active = $1 ORDER BY users.created_at DESC LIMIT 20`},
		{sqlgen.MySQL, "SELECT `users`.`id`, `users`.`name` FROM `users` JOIN orders ON orders.user_id = users.id WHERE users.active = ? ORDER BY users.created_at DESC LIMIT 20"},
		{sqlgen.MSSQL, `SELECT [users].[id], [users].[name] FROM [users] JOIN orders ON orders.user_id = users.id WHERE users.active = @p1 ORDER BY users.created_at DESC OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY`},
	}
	for _, c := range cases {
		t.Run(c.d.Name(), func(t *testing.T) {
			got, args, err := nonTrivialSelect().Build(c.d)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got != c.want {
				t.Errorf("SQL mismatch\n got: %s\nwant: %s", got, c.want)
			}
			if len(args) != 1 || args[0] != true {
				t.Errorf("args = %v, want [true]", args)
			}
		})
	}
}

func TestInsertUpsertReturning(t *testing.T) {
	mk := func() sqlgen.Insert {
		return sqlgen.Insert{
			Table:      "users",
			Columns:    []string{"name", "email"},
			Rows:       [][]any{{"alice", "a@x.io"}},
			OnConflict: &sqlgen.Conflict{Columns: []string{"email"}, Update: []string{"name"}},
			Returning:  []string{"id"},
		}
	}
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `INSERT INTO "users" ("name", "email") VALUES (?, ?) ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name" RETURNING "id"`},
		{sqlgen.Postgres, `INSERT INTO "users" ("name", "email") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name" RETURNING "id"`},
		{sqlgen.MySQL, "INSERT INTO `users` (`name`, `email`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)"},
	}
	for _, c := range cases {
		t.Run(c.d.Name(), func(t *testing.T) {
			got, args, err := mk().Build(c.d)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got != c.want {
				t.Errorf("SQL mismatch\n got: %s\nwant: %s", got, c.want)
			}
			if len(args) != 2 {
				t.Errorf("args = %v, want 2", args)
			}
		})
	}
}

// MSSQL renders a plain INSERT with OUTPUT, and an upsert as a MERGE.
func TestMSSQLInsertOutputAndMerge(t *testing.T) {
	out := sqlgen.Insert{
		Table:     "users",
		Columns:   []string{"name", "email"},
		Rows:      [][]any{{"alice", "a@x.io"}},
		Returning: []string{"id"},
	}
	got, _, err := out.Build(sqlgen.MSSQL)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := `INSERT INTO [users] ([name], [email]) OUTPUT INSERTED.[id] VALUES (@p1, @p2)`
	if got != want {
		t.Errorf("INSERT mismatch\n got: %s\nwant: %s", got, want)
	}

	upsert := out
	upsert.OnConflict = &sqlgen.Conflict{Columns: []string{"email"}, Update: []string{"name"}}
	gotM, args, err := upsert.Build(sqlgen.MSSQL)
	if err != nil {
		t.Fatalf("merge build: %v", err)
	}
	wantM := `MERGE INTO [users] AS tgt USING (VALUES (@p1, @p2)) AS src ([name], [email]) ON tgt.[email] = src.[email] WHEN MATCHED THEN UPDATE SET tgt.[name] = src.[name] WHEN NOT MATCHED THEN INSERT ([name], [email]) VALUES (src.[name], src.[email]) OUTPUT INSERTED.[id];`
	if gotM != wantM {
		t.Errorf("MERGE mismatch\n got: %s\nwant: %s", gotM, wantM)
	}
	if len(args) != 2 {
		t.Errorf("merge args = %v, want 2", args)
	}
}

// DoNothing renders ON CONFLICT DO NOTHING per dialect: a no-op self-assign on
// MySQL (which lacks DO NOTHING) and a MERGE with no WHEN MATCHED arm on MSSQL.
func TestUpsertDoNothing(t *testing.T) {
	mk := func() sqlgen.Insert {
		return sqlgen.Insert{
			Table:      "users",
			Columns:    []string{"name", "email"},
			Rows:       [][]any{{"alice", "a@x.io"}},
			OnConflict: &sqlgen.Conflict{Columns: []string{"email"}, Nothing: true},
		}
	}
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `INSERT INTO "users" ("name", "email") VALUES (?, ?) ON CONFLICT ("email") DO NOTHING`},
		{sqlgen.Postgres, `INSERT INTO "users" ("name", "email") VALUES ($1, $2) ON CONFLICT ("email") DO NOTHING`},
		{sqlgen.MySQL, "INSERT INTO `users` (`name`, `email`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `email` = `email`"},
		{sqlgen.MSSQL, `MERGE INTO [users] AS tgt USING (VALUES (@p1, @p2)) AS src ([name], [email]) ON tgt.[email] = src.[email] WHEN NOT MATCHED THEN INSERT ([name], [email]) VALUES (src.[name], src.[email]);`},
	}
	for _, c := range cases {
		t.Run(c.d.Name(), func(t *testing.T) {
			got, _, err := mk().Build(c.d)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got != c.want {
				t.Errorf("SQL mismatch\n got: %s\nwant: %s", got, c.want)
			}
		})
	}

	// A bare DO NOTHING (no conflict target) is valid on SQLite/Postgres; MySQL
	// cannot express it and must error rather than emit broken SQL.
	bare := sqlgen.Insert{Table: "t", Columns: []string{"a"}, Rows: [][]any{{1}}, OnConflict: &sqlgen.Conflict{Nothing: true}}
	if got, _, err := bare.Build(sqlgen.SQLite); err != nil || got != `INSERT INTO "t" ("a") VALUES (?) ON CONFLICT DO NOTHING` {
		t.Errorf("bare sqlite: got %q err %v", got, err)
	}
	if _, _, err := bare.Build(sqlgen.MySQL); err == nil {
		t.Error("bare DO NOTHING on MySQL should error (needs a conflict column)")
	}
}
