package conformance_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
	"liteorm.org/dialect/mssql"
	"liteorm.org/dialect/mysql"
	"liteorm.org/dialect/postgres"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// Account exercises: a unique column, a soft-delete column, a has-many relation,
// and a ctx-first BeforeCreate hook.
type Account struct {
	ID        int64
	Name      string
	Email     string `orm:"email,unique"`
	Note      string
	Active    bool         // exercises bool round-trip (SQLite has no bool type)
	CompanyID int64        `orm:"company_id"`
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
	Orders    []Order      // has-many; FK account_id on Order
	Company   *Company     // belongs-to; FK company_id on Account
	Profile   *Profile     // has-one; FK account_id on Profile (owner lacks the key)
	Roles     []Role       `orm:"m2m:account_roles"` // many-to-many via account_roles
}

func (Account) TableName() string { return "accounts" }

type Company struct {
	ID   int64
	Name string
}

func (Company) TableName() string { return "companies" }

type Role struct {
	ID   int64
	Name string
}

func (Role) TableName() string { return "roles" }

// Widget and its variants exercise introspection / ALTER-add sync / diff.
type Widget struct {
	ID    int64
	Name  string
	Color string
}

func (Widget) TableName() string { return "widgets" }

type widgetSeed struct {
	ID   int64
	Name string
}

func (widgetSeed) TableName() string { return "widgets" }

type widgetExtra struct {
	ID     int64
	Name   string
	Color  string
	Legacy string
}

func (widgetExtra) TableName() string { return "widgets" }

// widgetIndexed adds a secondary index on color, exercising additive index sync.
type widgetIndexed struct {
	ID    int64
	Name  string
	Color string `orm:"color,index"`
}

func (widgetIndexed) TableName() string { return "widgets" }

// typedV1 / typedV2 exercise reviewable type-change detection on one table:
// `code` goes from string to int64 between the two.
type typedV1 struct {
	ID   int64
	Code string
}

func (typedV1) TableName() string { return "typed_widgets" }

type typedV2 struct {
	ID   int64
	Code int64 // type changed: string -> int64
}

func (typedV2) TableName() string { return "typed_widgets" }

// fkParent / fkChild exercise opt-in FOREIGN KEY emission (WithForeignKeys).
type fkParent struct {
	ID   int64
	Name string
}

func (fkParent) TableName() string { return "fk_parents" }

type fkChild struct {
	ID       int64
	ParentID int64     `orm:"parent_id"`
	Parent   *fkParent `orm:"fk:parent_id"` // belongs-to
}

func (fkChild) TableName() string { return "fk_children" }

func (a *Account) BeforeCreate(_ context.Context, ev *orm.Event[Account]) error {
	ev.Model.Note = "hooked"
	return nil
}

// Compile-time proof the hook is wired (a wrong signature would fail to build).
var _ orm.BeforeCreateHook[Account] = (*Account)(nil)

type Order struct {
	ID        int64
	AccountID int64 `orm:"account_id"`
	Total     int64
}

func (Order) TableName() string { return "orders" }

// Profile is the has-one target of Account: the FK account_id lives here, not on
// Account, so the Account.Profile field resolves to has-one (belongs-to is tried
// first and falls through because Account has no profile_id).
type Profile struct {
	ID        int64
	AccountID int64 `orm:"account_id"`
	Bio       string
}

func (Profile) TableName() string { return "profiles" }

// countingSession counts QueryContext calls to prove eager loading is N+1-safe.
type countingSession struct {
	liteorm.Session
	queries int
}

func (c *countingSession) QueryContext(ctx context.Context, q string, args ...any) (liteorm.Rows, error) {
	c.queries++
	return c.Session.QueryContext(ctx, q, args...)
}

func TestORMSQLite(t *testing.T) {
	dir, err := os.MkdirTemp("", "liteorm-orm-*")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(dir, "orm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ormScenarios(t, db)
}

func TestORMPostgres(t *testing.T) {
	dsn := os.Getenv("LITEORM_PG_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_PG_DSN to run the Postgres orm suite")
	}
	db, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dropORMTables(t, db)
	ormScenarios(t, db)
}

// TestORMMySQL / TestORMMSSQL run the orm suite against MySQL / SQL Server when
// the respective DSN env var is set.
func TestORMMySQL(t *testing.T) {
	dsn := os.Getenv("LITEORM_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_MYSQL_DSN to run the MySQL orm suite")
	}
	db, err := mysql.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dropORMTables(t, db)
	ormScenarios(t, db)
}

func TestORMMSSQL(t *testing.T) {
	dsn := os.Getenv("LITEORM_MSSQL_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_MSSQL_DSN to run the MSSQL orm suite")
	}
	db, err := mssql.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dropORMTables(t, db)
	ormScenarios(t, db)
}

func dropORMTables(t *testing.T, db *liteorm.DB) {
	t.Helper()
	for _, tbl := range []string{"account_roles", "orders", "roles", "companies", "profiles", "widgets", "accounts"} {
		if _, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Fatal(err)
		}
	}
}

func ormScenarios(t *testing.T, db *liteorm.DB) {
	ctx := context.Background()
	if err := orm.AutoMigrate[Account](ctx, db); err != nil {
		t.Fatalf("automigrate Account: %v", err)
	}
	if err := orm.AutoMigrate[Order](ctx, db); err != nil {
		t.Fatalf("automigrate Order: %v", err)
	}
	if err := orm.AutoMigrate[Company](ctx, db); err != nil {
		t.Fatalf("automigrate Company: %v", err)
	}
	if err := orm.AutoMigrate[Role](ctx, db); err != nil {
		t.Fatalf("automigrate Role: %v", err)
	}
	if err := orm.AutoMigrate[Profile](ctx, db); err != nil {
		t.Fatalf("automigrate Profile: %v", err)
	}

	t.Run("CreateHookGetUpdate", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		a := Account{Name: "alice", Email: "alice@x.io"}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if a.ID == 0 {
			t.Fatal("Create did not set ID")
		}
		if a.Note != "hooked" {
			t.Errorf("BeforeCreate hook did not run: Note=%q", a.Note)
		}
		if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
			t.Errorf("autoCreate/Update time not stamped: created=%v updated=%v", a.CreatedAt, a.UpdatedAt)
		}
		got, err := repo.Get(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Note != "hooked" {
			t.Errorf("persisted Note = %q, want hooked", got.Note)
		}
		got.Name = "alice2"
		if err := repo.Update(ctx, &got); err != nil {
			t.Fatal(err)
		}
		if reread, _ := repo.Get(ctx, a.ID); reread.Name != "alice2" {
			t.Errorf("update not persisted: %+v", reread)
		}
	})

	t.Run("SoftDeleteTriStateAndUniqueIndexFix", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		a := Account{Name: "bob", Email: "bob@x.io"}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(ctx, &a); err != nil { // soft delete
			t.Fatal(err)
		}
		// Default scope hides it; IncludeDeleted/OnlyDeleted reveal it.
		if all, _ := repo.Find(ctx); len(all) != 0 {
			t.Errorf("WithoutDeleted returned %d, want 0", len(all))
		}
		if inc, _ := repo.IncludeDeleted().Find(ctx); len(inc) != 1 {
			t.Errorf("IncludeDeleted returned %d, want 1", len(inc))
		}
		if only, _ := repo.OnlyDeleted().Find(ctx); len(only) != 1 {
			t.Errorf("OnlyDeleted returned %d, want 1", len(only))
		}
		// The soft-delete unique-index fix: re-inserting the freed email succeeds.
		a2 := Account{Name: "bob2", Email: "bob@x.io"}
		if err := repo.Create(ctx, &a2); err != nil {
			t.Fatalf("re-insert after soft delete should succeed (partial unique index): %v", err)
		}
		// A live duplicate still violates the unique index. The classifier helper
		// is exercised alongside the errors.Is form it wraps.
		dup := Account{Name: "bob3", Email: "bob@x.io"}
		if err := repo.Create(ctx, &dup); !errors.Is(err, liteorm.ErrUniqueViolation) || !liteorm.IsUniqueViolation(err) {
			t.Fatalf("live duplicate: got %v, want ErrUniqueViolation", err)
		}
		// ForceDelete removes the row for real.
		if err := repo.ForceDelete(ctx, &a2); err != nil {
			t.Fatal(err)
		}
		if inc, _ := repo.IncludeDeleted().Find(ctx); len(inc) != 1 {
			t.Errorf("after ForceDelete, IncludeDeleted = %d, want 1 (only the soft-deleted bob)", len(inc))
		}
	})

	t.Run("EagerLoadN1Safe", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		a := Account{Name: "carol", Email: "carol@x.io"}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		b := Account{Name: "dave", Email: "dave@x.io"}
		if err := repo.Create(ctx, &b); err != nil {
			t.Fatal(err)
		}
		orderRepo := orm.NewRepo[Order](db)
		for i := range 3 {
			if err := orderRepo.Create(ctx, &Order{AccountID: a.ID, Total: int64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := orderRepo.Create(ctx, &Order{AccountID: b.ID, Total: 99}); err != nil {
			t.Fatal(err)
		}

		accs, err := repo.Find(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cs := &countingSession{Session: db}
		if err := orm.Load[Account, Order](ctx, cs, accs, "Orders"); err != nil {
			t.Fatal(err)
		}
		if cs.queries != 1 {
			t.Errorf("eager load used %d queries for %d parents, want 1 (N+1 regression)", cs.queries, len(accs))
		}
		byName := map[string]int{}
		for _, ac := range accs {
			byName[ac.Name] = len(ac.Orders)
		}
		if byName["carol"] != 3 || byName["dave"] != 1 {
			t.Errorf("eager-loaded order counts = %v, want carol:3 dave:1", byName)
		}
	})

	t.Run("BoolRoundTrip", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		a := Account{Name: "flagged", Email: "flag@x.io", Active: true}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.Get(ctx, a.ID); !got.Active {
			t.Errorf("bool did not round-trip: Active=%v", got.Active)
		}
	})

	t.Run("WriteConveniences", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)

		// Save: zero PK inserts, non-zero PK updates (no second row).
		a := Account{Name: "saver", Email: "saver@x.io"}
		if err := repo.Save(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if a.ID == 0 {
			t.Fatal("Save should insert and capture the PK")
		}
		a.Name = "saver2"
		if err := repo.Save(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.Get(ctx, a.ID); got.Name != "saver2" {
			t.Errorf("Save(update) name = %q, want saver2", got.Name)
		}
		if all, _ := repo.Find(ctx); len(all) != 1 {
			t.Errorf("Save(update) must not insert a second row, have %d", len(all))
		}

		// FirstOrCreate: existing row is loaded (not created); a new condition creates.
		existing := Account{Name: "ignored", Email: "saver@x.io"}
		created, err := repo.FirstOrCreate(ctx, &existing, query.Col[string]("email").Eq("saver@x.io"))
		if err != nil {
			t.Fatal(err)
		}
		if created || existing.ID != a.ID || existing.Name != "saver2" {
			t.Errorf("FirstOrCreate should load existing: created=%v %+v", created, existing)
		}
		fresh := Account{Name: "brand-new", Email: "new@x.io"}
		created, err = repo.FirstOrCreate(ctx, &fresh, query.Col[string]("email").Eq("new@x.io"))
		if err != nil || !created || fresh.ID == 0 {
			t.Fatalf("FirstOrCreate should create: created=%v id=%d err=%v", created, fresh.ID, err)
		}

		// Updates / Omit: only the chosen columns are written. The note still holds
		// the value the create hook stamped ("hooked"); Updates("name") must leave it.
		fresh.Name = "renamed"
		fresh.Note = "should-not-persist"
		if err := repo.Updates(ctx, &fresh, "name"); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.Get(ctx, fresh.ID); got.Name != "renamed" || got.Note != "hooked" {
			t.Errorf("Updates(\"name\") wrote the wrong columns: name=%q note=%q", got.Name, got.Note)
		}
		fresh.Note = "kept"
		if err := repo.Update(ctx, &fresh); err != nil { // persist the note
			t.Fatal(err)
		}
		fresh.Name = "renamed2"
		fresh.Note = "overwrite-attempt"
		if err := repo.Omit("Note").Update(ctx, &fresh); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.Get(ctx, fresh.ID); got.Name != "renamed2" || got.Note != "kept" {
			t.Errorf("Omit(\"Note\") should not have written note: name=%q note=%q", got.Name, got.Note)
		}

		// CreateInBatches: chunked multi-row insert, keys read back where supported.
		clean(t, db)
		batch := []*Account{
			{Name: "b1", Email: "b1@x.io"},
			{Name: "b2", Email: "b2@x.io"},
			{Name: "b3", Email: "b3@x.io"},
			{Name: "b4", Email: "b4@x.io"},
			{Name: "b5", Email: "b5@x.io"},
		}
		if err := repo.CreateInBatches(ctx, batch, 2); err != nil {
			t.Fatal(err)
		}
		if all, _ := repo.Find(ctx); len(all) != 5 {
			t.Errorf("CreateInBatches inserted %d rows, want 5", len(all))
		}
		// Generated keys are captured on RETURNING/OUTPUT dialects (every backend
		// here except MySQL, which has neither — see InsertManyCapturingPK).
		feat := db.Dialect().Features()
		if feat.Has(dialect.FeatReturning) || feat.Has(dialect.FeatOutput) {
			for i, v := range batch {
				if v.ID == 0 {
					t.Errorf("CreateInBatches row %d: PK not captured back", i)
				}
			}
		}
	})

	t.Run("FilteredReadsAndScopes", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		for i, name := range []string{"al", "bo", "cy", "di"} {
			a := Account{Name: name, Email: name + "@x.io", Active: i%2 == 0} // al, cy active
			if err := repo.Create(ctx, &a); err != nil {
				t.Fatal(err)
			}
		}

		// Where + OrderBy + Limit compose onto Find.
		got, err := repo.Where("name <> ?", "al").OrderBy("name DESC").Limit(2).Find(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Name != "di" || got[1].Name != "cy" {
			t.Errorf("Where/OrderBy/Limit returned %v", accountNames(got))
		}

		// Count and Exists honor the scope.
		if n, _ := repo.Where("active = ?", true).Count(ctx); n != 2 {
			t.Errorf("Count(active) = %d, want 2", n)
		}
		if ok, _ := repo.Where("name = ?", "nobody").Exists(ctx); ok {
			t.Error("Exists(nobody) = true, want false")
		}

		// First returns one row (or ErrNoRows).
		first, err := repo.OrderBy("name ASC").First(ctx)
		if err != nil || first.Name != "al" {
			t.Errorf("First(order name) = %q err=%v, want al", first.Name, err)
		}

		// A reusable scope packages a common filter.
		activeOnly := func(b *query.SelectBuilder[Account]) *query.SelectBuilder[Account] {
			return b.Where("active = ?", true)
		}
		active, err := repo.Scopes(activeOnly).OrderBy("name ASC").Find(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(active) != 2 || active[0].Name != "al" || active[1].Name != "cy" {
			t.Errorf("Scopes(activeOnly) returned %v", accountNames(active))
		}

		// Scopes compose with the soft-delete scope: a deleted active row is hidden.
		del := active[0]
		if err := repo.Delete(ctx, &del); err != nil {
			t.Fatal(err)
		}
		if n, _ := repo.Scopes(activeOnly).Count(ctx); n != 1 {
			t.Errorf("active count after soft-deleting one = %d, want 1", n)
		}
		if n, _ := repo.IncludeDeleted().Scopes(activeOnly).Count(ctx); n != 2 {
			t.Errorf("IncludeDeleted active count = %d, want 2", n)
		}
	})

	t.Run("AssociationWrites", func(t *testing.T) {
		clean(t, db)
		accounts := orm.NewRepo[Account](db)
		orders := orm.NewRepo[Order](db)
		roles := orm.NewRepo[Role](db)

		a := Account{Name: "owner", Email: "owner@x.io"}
		if err := accounts.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}

		// Belongs-to is rejected by the association handle (it is a single FK).
		if _, err := orm.Assoc[Account, Company](db, "Company", &a); err == nil {
			t.Error("Assoc on a belongs-to relation should error")
		}

		// --- has-many: Orders, FK account_id on orders ---
		o1 := Order{Total: 1}
		o2 := Order{Total: 2}
		o3 := Order{Total: 3}
		for _, o := range []*Order{&o1, &o2, &o3} {
			if err := orders.Create(ctx, o); err != nil { // created detached (account_id 0)
				t.Fatal(err)
			}
		}
		hm, err := orm.Assoc[Account, Order](db, "Orders", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := hm.Append(ctx, &o1, &o2); err != nil {
			t.Fatal(err)
		}
		if o1.AccountID != a.ID { // Append writes the FK back into the target in memory
			t.Errorf("Append did not set in-memory FK: o1.AccountID=%d want %d", o1.AccountID, a.ID)
		}
		if n, _ := hm.Count(ctx); n != 2 {
			t.Errorf("has-many Count after Append = %d, want 2", n)
		}
		if err := hm.Delete(ctx, &o1); err != nil { // detach o1 (account_id → NULL)
			t.Fatal(err)
		}
		if o1.AccountID != 0 {
			t.Errorf("Delete did not zero in-memory FK: o1.AccountID=%d", o1.AccountID)
		}
		if n, _ := hm.Count(ctx); n != 1 {
			t.Errorf("has-many Count after Delete = %d, want 1", n)
		}
		if err := hm.Replace(ctx, &o3); err != nil { // set becomes exactly {o3}
			t.Fatal(err)
		}
		if n, _ := hm.Count(ctx); n != 1 {
			t.Errorf("has-many Count after Replace = %d, want 1", n)
		}
		if err := hm.Clear(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ := hm.Count(ctx); n != 0 {
			t.Errorf("has-many Count after Clear = %d, want 0", n)
		}

		// --- many-to-many: Roles via account_roles ---
		admin := Role{Name: "admin"}
		user := Role{Name: "user"}
		guest := Role{Name: "guest"}
		for _, r := range []*Role{&admin, &user, &guest} {
			if err := roles.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		m2m, err := orm.Assoc[Account, Role](db, "Roles", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := m2m.Append(ctx, &admin, &user); err != nil {
			t.Fatal(err)
		}
		if err := m2m.Append(ctx, &admin); err != nil { // idempotent re-link
			t.Fatal(err)
		}
		if n, _ := m2m.Count(ctx); n != 2 {
			t.Errorf("m2m Count after Append = %d, want 2", n)
		}
		if err := m2m.Delete(ctx, &user); err != nil {
			t.Fatal(err)
		}
		if n, _ := m2m.Count(ctx); n != 1 {
			t.Errorf("m2m Count after Delete = %d, want 1", n)
		}
		if err := m2m.Replace(ctx, &user, &guest); err != nil {
			t.Fatal(err)
		}
		if n, _ := m2m.Count(ctx); n != 2 {
			t.Errorf("m2m Count after Replace = %d, want 2", n)
		}
		// Load reflects the replaced set.
		accs := []Account{a}
		if err := orm.Load[Account, Role](ctx, db, accs, "Roles"); err != nil {
			t.Fatal(err)
		}
		gotRoles := map[string]bool{}
		for _, r := range accs[0].Roles {
			gotRoles[r.Name] = true
		}
		if !gotRoles["user"] || !gotRoles["guest"] || gotRoles["admin"] {
			t.Errorf("m2m Replace set wrong: %v", gotRoles)
		}
		if err := m2m.Clear(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ := m2m.Count(ctx); n != 0 {
			t.Errorf("m2m Count after Clear = %d, want 0", n)
		}
	})

	t.Run("BelongsToEagerLoad", func(t *testing.T) {
		clean(t, db)
		comp := Company{Name: "acme"}
		if err := orm.NewRepo[Company](db).Create(ctx, &comp); err != nil {
			t.Fatal(err)
		}
		a := Account{Name: "frank", Email: "frank@x.io", CompanyID: comp.ID}
		if err := orm.NewRepo[Account](db).Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		accs, err := orm.NewRepo[Account](db).Find(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cs := &countingSession{Session: db}
		if err := orm.Load[Account, Company](ctx, cs, accs, "Company"); err != nil {
			t.Fatal(err)
		}
		if cs.queries != 1 {
			t.Errorf("belongs-to load used %d queries, want 1", cs.queries)
		}
		if accs[0].Company == nil || accs[0].Company.Name != "acme" {
			t.Errorf("belongs-to not loaded: %+v", accs[0].Company)
		}
	})

	t.Run("HasOne", func(t *testing.T) {
		clean(t, db)
		accounts := orm.NewRepo[Account](db)
		profiles := orm.NewRepo[Profile](db)

		a := Account{Name: "han", Email: "han@x.io"}
		if err := accounts.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		p := Profile{AccountID: a.ID, Bio: "hello"}
		if err := profiles.Create(ctx, &p); err != nil {
			t.Fatal(err)
		}

		// Eager-load the has-one in one batched query, assigned as a single value.
		list := []Account{a}
		cs := &countingSession{Session: db}
		if err := orm.Load[Account, Profile](ctx, cs, list, "Profile"); err != nil {
			t.Fatal(err)
		}
		if cs.queries != 1 {
			t.Errorf("has-one load used %d queries, want 1", cs.queries)
		}
		if list[0].Profile == nil || list[0].Profile.Bio != "hello" {
			t.Fatalf("has-one not loaded: %+v", list[0].Profile)
		}

		// The association handle manages a has-one through the same FK path; Replace
		// is the natural setter (clears the old target, points the new one at us).
		p2 := Profile{Bio: "replaced"}
		if err := profiles.Create(ctx, &p2); err != nil { // created detached
			t.Fatal(err)
		}
		rel, err := orm.Assoc[Account, Profile](db, "Profile", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := rel.Replace(ctx, &p2); err != nil {
			t.Fatal(err)
		}
		if n, _ := rel.Count(ctx); n != 1 {
			t.Errorf("has-one Count after Replace = %d, want 1", n)
		}
		reload := []Account{a}
		if err := orm.Load[Account, Profile](ctx, db, reload, "Profile"); err != nil {
			t.Fatal(err)
		}
		if reload[0].Profile == nil || reload[0].Profile.Bio != "replaced" {
			t.Errorf("Replace did not swap the has-one target: %+v", reload[0].Profile)
		}
		if err := rel.Clear(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ := rel.Count(ctx); n != 0 {
			t.Errorf("has-one Count after Clear = %d, want 0", n)
		}
	})

	t.Run("ManyToMany", func(t *testing.T) {
		clean(t, db)
		repo := orm.NewRepo[Account](db)
		roleRepo := orm.NewRepo[Role](db)
		a := Account{Name: "grace", Email: "grace@x.io"}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		admin := Role{Name: "admin"}
		user := Role{Name: "user"}
		if err := roleRepo.Create(ctx, &admin); err != nil {
			t.Fatal(err)
		}
		if err := roleRepo.Create(ctx, &user); err != nil {
			t.Fatal(err)
		}
		if err := orm.Attach[Account, Role](ctx, db, "Roles", &a, &admin, &user); err != nil {
			t.Fatal(err)
		}
		accs, err := repo.Find(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cs := &countingSession{Session: db}
		if err := orm.Load[Account, Role](ctx, cs, accs, "Roles"); err != nil {
			t.Fatal(err)
		}
		if cs.queries != 1 {
			t.Errorf("m2m load used %d queries, want 1 (single JOIN)", cs.queries)
		}
		if len(accs[0].Roles) != 2 {
			t.Errorf("m2m loaded %d roles, want 2", len(accs[0].Roles))
		}
	})

	t.Run("IntrospectAlterAndMigration", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS widgets"); err != nil {
			t.Fatal(err)
		}
		// ALTER-add sync: a partial table, then AutoMigrate the fuller model.
		if err := orm.AutoMigrate[widgetSeed](ctx, db); err != nil { // id, name
			t.Fatal(err)
		}
		if err := orm.AutoMigrate[Widget](ctx, db); err != nil { // id, name, color -> ADD color
			t.Fatal(err)
		}
		cols, err := orm.IntrospectColumns(ctx, db, "widgets")
		if err != nil {
			t.Fatal(err)
		}
		if !hasCol(cols, "color") {
			t.Errorf("AutoMigrate did not ADD COLUMN color: have %v", colNames(cols))
		}

		// Destructive diff: an extra DB column the model lacks.
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS widgets"); err != nil {
			t.Fatal(err)
		}
		if err := orm.AutoMigrate[widgetExtra](ctx, db); err != nil { // id,name,color,legacy
			t.Fatal(err)
		}
		ch, err := orm.Diff[Widget](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !removed(ch, "legacy") {
			t.Errorf("Diff.Removed = %v, want it to include legacy", ch.Removed)
		}
		up, _, err := orm.GenerateMigration[Widget](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(up, "legacy") || !strings.Contains(strings.ToUpper(up), "DROP COLUMN") {
			t.Errorf("GenerateMigration up = %q, want a destructive DROP of legacy", up)
		}
		// The generator must NOT execute: legacy is still present.
		after, _ := orm.IntrospectColumns(ctx, db, "widgets")
		if !hasCol(after, "legacy") {
			t.Error("GenerateMigration must not execute SQL (legacy was dropped)")
		}
	})

	t.Run("IndexAddSync", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS widgets"); err != nil {
			t.Fatal(err)
		}
		// Create the table without the index, then re-migrate the model that adds it.
		if err := orm.AutoMigrate[Widget](ctx, db); err != nil { // id, name, color (no index)
			t.Fatal(err)
		}
		before, err := orm.IntrospectIndexes(ctx, db, "widgets")
		if err != nil {
			t.Fatal(err)
		}
		if hasIndex(before, "ix_widgets_color") {
			t.Fatalf("ix_widgets_color should not exist yet: %v", before)
		}
		if err := orm.AutoMigrate[widgetIndexed](ctx, db); err != nil { // adds index on color
			t.Fatal(err)
		}
		after, err := orm.IntrospectIndexes(ctx, db, "widgets")
		if err != nil {
			t.Fatal(err)
		}
		if !hasIndex(after, "ix_widgets_color") {
			t.Errorf("AutoMigrate did not create the new index: have %v", after)
		}
		// Idempotent: a second migrate of the same model must not error (no duplicate).
		if err := orm.AutoMigrate[widgetIndexed](ctx, db); err != nil {
			t.Errorf("re-migrate should be a no-ev, got %v", err)
		}
	})

	t.Run("TypeChangeIsReviewableOnly", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS typed_widgets"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS typed_widgets") })
		if err := orm.AutoMigrate[typedV1](ctx, db); err != nil { // code is a string column
			t.Fatal(err)
		}
		// No false positive: the same model vs its own live table reports no change,
		// even though the catalog spells types differently than the emitted DDL (and
		// the auto-increment id introspects as a plain integer, not SERIAL/IDENTITY).
		same, err := orm.Diff[typedV1](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(same.Changed) != 0 {
			t.Errorf("identity diff flagged spurious type changes: %+v", same.Changed)
		}

		// A genuine change (string -> int64 on code) is detected.
		ch, err := orm.Diff[typedV2](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(ch.Changed) != 1 || !strings.EqualFold(ch.Changed[0].Column, "code") {
			t.Fatalf("Diff.Changed = %+v, want one change on code", ch.Changed)
		}

		// It surfaces in GenerateMigration as a reviewable (commented) statement...
		up, _, err := orm.GenerateMigration[typedV2](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(up, "code") || !strings.Contains(strings.ToLower(up), "type change") {
			t.Errorf("GenerateMigration up = %q, want a reviewable type change for code", up)
		}
		for line := range strings.SplitSeq(up, "\n") {
			if strings.Contains(line, "code") && !strings.HasPrefix(strings.TrimSpace(line), "--") {
				t.Errorf("type change must be commented (not executable): %q", line)
			}
		}
		// ...and is NOT applied: the change is still pending afterwards.
		again, err := orm.Diff[typedV2](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.Changed) != 1 {
			t.Errorf("GenerateMigration must not apply the type change, Changed = %+v", again.Changed)
		}
	})

	t.Run("ForeignKeyConstraintOptIn", func(t *testing.T) {
		dropFK := func() {
			for _, tbl := range []string{"fk_children", "fk_parents"} {
				_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl)
			}
		}
		dropFK()
		t.Cleanup(dropFK)
		// Migrate the referenced table first, then the child with FK emission on.
		if err := orm.AutoMigrate[fkParent](ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := orm.AutoMigrate[fkChild](ctx, db, orm.WithForeignKeys()); err != nil {
			t.Fatal(err)
		}
		children := orm.NewRepo[fkChild](db)
		// A child pointing at a non-existent parent violates the constraint.
		if err := children.Create(ctx, &fkChild{ParentID: 999999}); err == nil {
			t.Error("expected an FK violation for a child referencing a missing parent")
		}
		// A child pointing at a real parent succeeds.
		p := fkParent{Name: "p"}
		if err := orm.NewRepo[fkParent](db).Create(ctx, &p); err != nil {
			t.Fatal(err)
		}
		if err := children.Create(ctx, &fkChild{ParentID: p.ID}); err != nil {
			t.Errorf("valid FK insert failed: %v", err)
		}
	})

	t.Run("InteropOrmAndQueryOnOneTx", func(t *testing.T) {
		clean(t, db)
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Write via the orm front-end...
		a := Account{Name: "erin", Email: "erin@x.io"}
		if err := orm.NewRepo[Account](tx).Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		// ...and read it back via the explicit query front-end on the SAME tx.
		out, err := query.Select[Account](tx).Where("id = ?", a.ID).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Name != "erin" {
			t.Errorf("interop read = %v", out)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func hasCol(cols []orm.ColumnMeta, name string) bool {
	for _, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func hasIndex(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return true
		}
	}
	return false
}

func colNames(cols []orm.ColumnMeta) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func removed(ch orm.Changes, name string) bool {
	for _, c := range ch.Removed {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func clean(t *testing.T, db *liteorm.DB) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range []string{"account_roles", "orders", "roles", "companies", "profiles", "accounts"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}
}

func accountNames(as []Account) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}
