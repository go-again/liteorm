package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
	"liteorm.org/query"
)

// Cross-dialect coverage for the typed predicates/fields that retire raw SQL
// fragments (the pantry "zero raw SQL" filing). DoNothing lives in upsert_test.go;
// query.Match is SQLite-only and is covered in dialect/sqlite. Each subtest scopes
// its rows with a "zrs-" sentinel so it does not collide with the shared suite data.

func TestZeroRawSQLPredicates(t *testing.T) {
	ctx := context.Background()
	users := orm.NewRepo[User](DB)

	t.Run("HasPrefix escapes wildcards", func(t *testing.T) {
		for _, n := range []string{"zrs-pre-a", "zrs-pre-b", "zrs-zzz", "zrs-pct-100%off", "zrs-pct-100xoff"} {
			if err := users.Create(ctx, &User{Name: n}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := query.Select[User](DB).Filter(query.Col[string]("name").HasPrefix("zrs-pre")).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("HasPrefix(zrs-pre) = %d rows, want 2", len(got))
		}
		// The literal % must be matched literally, not as a wildcard.
		esc, err := query.Select[User](DB).Filter(query.Col[string]("name").HasPrefix("zrs-pct-100%")).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(esc) != 1 || esc[0].Name != "zrs-pct-100%off" {
			t.Errorf("HasPrefix(zrs-pct-100%%) = %v, want only zrs-pct-100%%off", names(esc))
		}
	})

	t.Run("Inc and PluckExpr", func(t *testing.T) {
		u := &User{Name: "zrs-counter", Age: 5}
		if err := users.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := query.Update[User](DB).Inc("age", 1).Where("id = ?", u.ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		got, err := users.Get(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Age != 7 {
			t.Errorf("Age after two Inc = %d, want 7", got.Age)
		}
		max, err := query.PluckExprFirst[User, int64](ctx,
			query.Select[User](DB).Filter(query.Col[string]("name").HasPrefix("zrs-counter")), "MAX(age)")
		if err != nil {
			t.Fatal(err)
		}
		if max != 7 {
			t.Errorf("MAX(age) = %d, want 7", max)
		}
	})

	t.Run("ExistsField correlated projection", func(t *testing.T) {
		yes := &User{Name: "zrs-acct-yes"}
		no := &User{Name: "zrs-acct-no"}
		if err := users.Create(ctx, yes); err != nil {
			t.Fatal(err)
		}
		if err := users.Create(ctx, no); err != nil {
			t.Fatal(err)
		}
		if err := orm.NewRepo[Account](DB).Create(ctx, &Account{UserID: yes.ID, Number: "zrs-1"}); err != nil {
			t.Fatal(err)
		}

		hasAccount := query.ExistsField("has_account",
			query.Select[Account](DB).Filter(
				query.Col[int64]("user_id").EqCol(query.Col[int64]("id").Of("users"))))

		type row struct {
			ID         int64
			Name       string
			HasAccount bool
		}
		rows, err := query.Into[User, row](ctx,
			query.Select[User](DB).Filter(query.Col[int64]("id").In(yes.ID, no.ID)).OrderBy("id"),
			query.Name("id"), query.Name("name"), hasAccount)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || !rows[0].HasAccount || rows[1].HasAccount {
			t.Errorf("ExistsField rows = %+v, want yes=true no=false", rows)
		}
	})
}

func names(us []User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}
