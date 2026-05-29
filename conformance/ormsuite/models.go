// Package ormsuite is an integration test suite for liteorm's declarative orm
// front-end, organized by topic (create, query, update, delete, associations,
// preload, hooks, soft delete, scopes, transactions, count, upsert) over one
// shared set of models — modeled on gorm's gorm.io/gorm/tests, adapted to
// liteorm's explicit semantics: associations are has-one / has-many / belongs-to
// / many-to-many / self-referential (no polymorphic), composite primary keys are
// supported, and writes never cascade a graph (you persist associations
// explicitly).
//
// The suite opens one backend per `go test` run (SQLite by default; set
// LITEORM_DIALECT=postgres|mysql|mssql with the matching DSN env to run it
// against a server database — see suite_test.go).
package ormsuite

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// User is the hub model. It has one Account, many Pets, belongs to a Company and
// to a Manager (self-referential), leads a Team (self-referential has-many), and
// speaks many Languages (many-to-many). It carries auto timestamps and a
// soft-delete column (liteorm's equivalent of embedding gorm.Model).
type User struct {
	ID        int64
	Name      string
	Age       int64
	Active    bool
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`

	Account   *Account   // has-one: FK user_id on accounts
	Pets      []Pet      // has-many: FK user_id on pets
	CompanyID int64      `orm:"company_id"`
	Company   *Company   // belongs-to: FK company_id on users
	ManagerID int64      `orm:"manager_id"`
	Manager   *User      `orm:"fk:manager_id"` // belongs-to self
	Team      []User     `orm:"fk:manager_id"` // has-many self
	Languages []Language `orm:"m2m:user_languages"`
	Toys      []Toy      `orm:"polymorphic:Owner"` // polymorphic: toys.owner_id/owner_type
}

func (User) TableName() string { return "users" }

// Account is the has-one target of User (the FK user_id lives here).
type Account struct {
	ID        int64
	UserID    int64 `orm:"user_id"`
	Number    string
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
}

func (Account) TableName() string { return "accounts" }

// Pet is a has-many target of User. It also owns Toys polymorphically, so the
// same toys table is shared with User.
type Pet struct {
	ID        int64
	UserID    int64 `orm:"user_id"`
	Name      string
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
	Toys      []Toy        `orm:"polymorphic:Owner"`
}

func (Pet) TableName() string { return "pets" }

// Toy is owned polymorphically by a User or a Pet via (owner_id, owner_type) —
// the canonical gorm polymorphic shape. owner_id is nullable so detaching a toy
// nulls the link without deleting the row.
type Toy struct {
	ID        int64
	Name      string
	OwnerID   sql.NullInt64 `orm:"owner_id"`
	OwnerType string        `orm:"owner_type"`
}

func (Toy) TableName() string { return "toys" }

// Company is the belongs-to target of User (no soft delete, like gorm's Company).
type Company struct {
	ID   int64
	Name string
}

func (Company) TableName() string { return "companies" }

// Language is the many-to-many target of User, linked via the user_languages
// junction (created by AutoMigrate).
type Language struct {
	ID   int64
	Code string `orm:"code,unique"`
	Name string
}

func (Language) TableName() string { return "languages" }

// Membership has a composite primary key (tenant_id, user_id) — a natural-key
// link row, the headline composite-PK case.
type Membership struct {
	TenantID int64 `orm:"tenant_id,pk"`
	UserID   int64 `orm:"user_id,pk"`
	Role     string
}

func (Membership) TableName() string { return "memberships" }

// CSV is a custom scalar type stored as a comma-separated string — it implements
// driver.Valuer (write) and sql.Scanner (read), so liteorm treats it as a plain
// column rather than a relation. This is the bun/xorm "custom type / conversion"
// pattern, portable across every backend (it lands in a text column).
type CSV []string

func (c CSV) Value() (driver.Value, error) { return strings.Join(c, ","), nil }

func (c *CSV) Scan(v any) error {
	switch s := v.(type) {
	case nil:
		*c = nil
	case string:
		*c = splitCSV(s)
	case []byte:
		*c = splitCSV(string(s))
	default:
		return fmt.Errorf("CSV.Scan: unsupported type %T", v)
	}
	return nil
}

func splitCSV(s string) CSV {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// JSONMap is a custom scalar type stored as a JSON object — the bun/xorm "JSON
// column" pattern, again portable (a text column holding JSON).
type JSONMap map[string]string

func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

func (m *JSONMap) Scan(v any) error {
	var b []byte
	switch s := v.(type) {
	case nil:
		*m = nil
		return nil
	case string:
		b = []byte(s)
	case []byte:
		b = s
	default:
		return fmt.Errorf("JSONMap.Scan: unsupported type %T", v)
	}
	return json.Unmarshal(b, m)
}

// Event exercises custom scalar types: Labels round-trips as CSV text, Props as a
// JSON object. Both implement Valuer/Scanner, so they ride in ordinary columns.
type Event struct {
	ID     int64
	Name   string
	Labels CSV     `orm:"labels"`
	Props  JSONMap `orm:"props"`
}

func (Event) TableName() string { return "events" }
