package ormsuite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/dialect/mssql"
	"liteorm.org/dialect/mysql"
	"liteorm.org/dialect/postgres"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
)

// DB is the backend under test, opened once by TestMain and shared by every test
// (gorm's tests work the same way). SQLite is the default; set
// LITEORM_DIALECT=postgres|mysql|mssql with the matching DSN env to run the whole
// suite against a server database instead.
var DB *liteorm.DB

func TestMain(m *testing.M) {
	db, cleanup, err := openDB()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ormsuite: open:", err)
		os.Exit(1)
	}
	DB = db
	if err := migrateAll(context.Background(), db); err != nil {
		fmt.Fprintln(os.Stderr, "ormsuite: migrate:", err)
		cleanup()
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func openDB() (*liteorm.DB, func() error, error) {
	ctx := context.Background()
	switch os.Getenv("LITEORM_DIALECT") {
	case "postgres":
		db, err := postgres.Open(ctx, mustDSN("LITEORM_PG_DSN"))
		return db, db.Close, err
	case "mysql":
		db, err := mysql.Open(ctx, mustDSN("LITEORM_MYSQL_DSN"))
		return db, db.Close, err
	case "mssql":
		db, err := mssql.Open(ctx, mustDSN("LITEORM_MSSQL_DSN"))
		return db, db.Close, err
	default:
		dir, err := os.MkdirTemp("", "liteorm-ormsuite-*")
		if err != nil {
			return nil, func() error { return nil }, err
		}
		db, err := sqlite.Open(filepath.Join(dir, "suite.db"))
		return db, func() error { _ = db.Close(); return os.RemoveAll(dir) }, err
	}
}

func mustDSN(env string) string {
	dsn := os.Getenv(env)
	if dsn == "" {
		fmt.Fprintf(os.Stderr, "ormsuite: %s must be set for the selected dialect\n", env)
		os.Exit(1)
	}
	return dsn
}

// migrateAll drops and recreates the suite's tables, so a server database starts
// from a clean schema each run.
func migrateAll(ctx context.Context, db *liteorm.DB) error {
	for _, t := range []string{"user_languages", "accounts", "pets", "users", "languages", "companies", "memberships", "toys", "events", "docs"} {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+t)
	}
	// One-liner migration of the whole model set, in dependency order (referenced
	// tables first); User also creates the user_languages junction.
	return orm.AutoMigrateAll(ctx, db,
		Company{}, Language{}, User{}, Account{}, Pet{}, Membership{}, Toy{}, Event{}, Doc{})
}

// --- assertions (ported in spirit from gorm.io/gorm/utils/tests) ---

// AssertEqual fails unless got and expect are deeply equal, with a tolerance for
// time.Time rounding (timestamps round-trip to second precision on some drivers).
func AssertEqual(t *testing.T, got, expect any) {
	t.Helper()
	if reflect.DeepEqual(got, expect) {
		return
	}
	if g, ok := got.(time.Time); ok {
		if e, ok := expect.(time.Time); ok && g.Round(time.Second).UTC().Equal(e.Round(time.Second).UTC()) {
			return
		}
	}
	if fmt.Sprint(got) == fmt.Sprint(expect) {
		return
	}
	t.Errorf("expect: %#v, got: %#v", expect, got)
}

// AssertObjEqual compares the named exported fields of two structs (or pointers).
func AssertObjEqual(t *testing.T, got, expect any, fields ...string) {
	t.Helper()
	gv := reflect.Indirect(reflect.ValueOf(got))
	ev := reflect.Indirect(reflect.ValueOf(expect))
	for _, name := range fields {
		AssertEqual(t, gv.FieldByName(name).Interface(), ev.FieldByName(name).Interface())
	}
}

// --- model builders (the Config-driven GetUser, like gorm's helper_test.go) ---

// Config selects which associations GetUser/seedUser populate.
type Config struct {
	Account   bool
	Pets      int
	Company   bool
	Manager   bool
	Team      int
	Languages int
}

// GetUser builds an in-memory User graph (nothing is persisted). Use seedUser to
// persist it.
func GetUser(name string, c Config) *User {
	u := &User{Name: name, Age: 18, Active: true}
	if c.Account {
		u.Account = &Account{Number: name + "_account"}
	}
	for i := range c.Pets {
		u.Pets = append(u.Pets, Pet{Name: fmt.Sprintf("%s_pet_%d", name, i+1)})
	}
	if c.Company {
		u.Company = &Company{Name: "company-" + name}
	}
	if c.Manager {
		u.Manager = GetUser(name+"_manager", Config{})
	}
	for i := range c.Team {
		u.Team = append(u.Team, *GetUser(fmt.Sprintf("%s_team_%d", name, i+1), Config{}))
	}
	for i := range c.Languages {
		n := fmt.Sprintf("%s_lang_%d", name, i+1)
		u.Languages = append(u.Languages, Language{Code: n, Name: n})
	}
	return u
}

// mustCreate inserts v, failing the test on error.
func mustCreate[T any](t *testing.T, v *T) {
	t.Helper()
	if err := orm.NewRepo[T](DB).Create(context.Background(), v); err != nil {
		t.Fatalf("create %T: %v", v, err)
	}
}

// seedUser persists a User and the associations selected by c — explicitly, since
// liteorm never cascades a write. It returns the user with all generated keys set.
func seedUser(t *testing.T, name string, c Config) *User {
	t.Helper()
	ctx := context.Background()
	u := GetUser(name, c)

	if u.Company != nil {
		mustCreate(t, u.Company)
		u.CompanyID = u.Company.ID
	}
	if u.Manager != nil {
		mustCreate(t, u.Manager)
		u.ManagerID = u.Manager.ID
	}
	mustCreate(t, u)

	if u.Account != nil {
		u.Account.UserID = u.ID
		mustCreate(t, u.Account)
	}
	for i := range u.Pets {
		u.Pets[i].UserID = u.ID
		mustCreate(t, &u.Pets[i])
	}
	for i := range u.Team {
		u.Team[i].ManagerID = u.ID
		mustCreate(t, &u.Team[i])
	}
	if len(u.Languages) > 0 {
		ptrs := make([]*Language, len(u.Languages))
		for i := range u.Languages {
			mustCreate(t, &u.Languages[i])
			ptrs[i] = &u.Languages[i]
		}
		rel, err := orm.Assoc[User, Language](DB, "Languages", u)
		if err != nil {
			t.Fatal(err)
		}
		if err := rel.Append(ctx, ptrs...); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

// CheckUser re-reads got's row and asserts its scalar fields match expect.
func CheckUser(t *testing.T, got, expect *User) {
	t.Helper()
	AssertObjEqual(t, got, expect, "ID", "Name", "Age", "Active", "CompanyID", "ManagerID")
}
