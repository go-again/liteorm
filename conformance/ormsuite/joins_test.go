package ormsuite

import (
	"context"
	"database/sql"
	"testing"

	"liteorm.org/query"
)

// TestInnerJoin joins users to their company and projects across both tables — the
// thing orm.Repo can't express, so you drop to the query builder on the same DB
// (gorm Joins, xorm Join).
func TestInnerJoin(t *testing.T) {
	ctx := context.Background()
	acme := &Company{Name: "join-acme"}
	mustCreate(t, acme)
	mustCreate(t, &User{Name: "join-u1", CompanyID: acme.ID})
	mustCreate(t, &User{Name: "join-u2", CompanyID: acme.ID})
	mustCreate(t, &User{Name: "join-orphan"}) // no company

	type row struct {
		UserName    string `db:"user_name"`
		CompanyName string `db:"company_name"`
	}
	rows, err := query.Into[User, row](ctx,
		query.Select[User](DB).
			InnerJoin("companies", "companies.id = users.company_id").
			Where("users.name LIKE ?", "join-u%").
			OrderBy("users.name ASC"),
		query.Expr("users.name AS user_name"),
		query.Expr("companies.name AS company_name"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the two users with a company match; the orphan is excluded by the inner join.
	if len(rows) != 2 || rows[0].UserName != "join-u1" || rows[0].CompanyName != "join-acme" {
		t.Errorf("inner join rows = %+v, want 2 join-acme members", rows)
	}
}

// TestLeftJoin keeps the unmatched left row, with a NULL right side.
func TestLeftJoin(t *testing.T) {
	ctx := context.Background()
	mustCreate(t, &User{Name: "lj-orphan"}) // company_id 0 → no match

	type row struct {
		UserName    string         `db:"user_name"`
		CompanyName sql.NullString `db:"company_name"`
	}
	rows, err := query.Into[User, row](ctx,
		query.Select[User](DB).
			LeftJoin("companies", "companies.id = users.company_id").
			Where("users.name = ?", "lj-orphan"),
		query.Expr("users.name AS user_name"),
		query.Expr("companies.name AS company_name"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CompanyName.Valid {
		t.Errorf("left join orphan = %+v, want one row with a NULL company", rows)
	}
}
