// Command fixtures shows two patterns borrowed from other ORMs, done the liteorm
// way: declarative, transactional **fixture seeding** (a graph persisted in
// dependency order, references resolved as ordinary Go), and **multi-tenant
// scoping** without a hidden global filter — an explicit reusable scope on reads
// plus a context-driven stamp on writes. It runs against a throwaway SQLite
// database and cleans up after itself.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

// --- models: a tiny multi-tenant app (orgs own members and projects) ---

type Org struct {
	ID   int64
	Name string
}

func (Org) TableName() string { return "orgs" }

type Member struct {
	ID    int64
	OrgID int64 `orm:"org_id"` // tenant key
	Name  string
}

func (Member) TableName() string { return "members" }

// BeforeCreate stamps the tenant (org) from context when the caller left it unset.
func (m *Member) BeforeCreate(ctx context.Context, op *orm.Op[Member]) error {
	if op.Model.OrgID == 0 {
		if org, ok := orgFrom(ctx); ok {
			op.Model.OrgID = org
		}
	}
	return nil
}

type Project struct {
	ID    int64
	OrgID int64 `orm:"org_id"`
	Title string
}

func (Project) TableName() string { return "projects" }

func (p *Project) BeforeCreate(ctx context.Context, op *orm.Op[Project]) error {
	if op.Model.OrgID == 0 {
		if org, ok := orgFrom(ctx); ok {
			op.Model.OrgID = org
		}
	}
	return nil
}

// --- multi-tenant plumbing: context carries the org; scopes confine reads ---

type orgKey struct{}

func withOrg(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, orgKey{}, id)
}
func orgFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(orgKey{}).(int64)
	return id, ok
}

func membersOfOrg(id int64) orm.Scope[Member] {
	return func(b *query.SelectBuilder[Member]) *query.SelectBuilder[Member] {
		return b.Filter(query.Col[int64]("org_id").Eq(id))
	}
}
func projectsOfOrg(id int64) orm.Scope[Project] {
	return func(b *query.SelectBuilder[Project]) *query.SelectBuilder[Project] {
		return b.Filter(query.Col[int64]("org_id").Eq(id))
	}
}

// --- fixture seeding: ordered steps in one transaction (atomic) ---

type seedStep func(ctx context.Context, sess liteorm.Session) error

// seed runs steps in order inside a single transaction; if any step fails the
// whole fixture set rolls back, so the database is never half-seeded.
func seed(ctx context.Context, db *liteorm.DB, steps ...seedStep) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if err := step(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}
	return tx.Commit(ctx)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-fixtures-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	if err := orm.AutoMigrateAll(ctx, db, Org{}, Member{}, Project{}); err != nil {
		return err
	}

	// ---- Declarative fixture seed: orgs, then their members + projects ----
	section("Fixture seed (one transaction, references resolved in Go)")
	var acme, globex Org
	err = seed(ctx, db,
		func(ctx context.Context, sess liteorm.Session) error {
			acme = Org{Name: "Acme"}
			globex = Org{Name: "Globex"}
			r := orm.NewRepo[Org](sess)
			if err := r.Create(ctx, &acme); err != nil {
				return err
			}
			return r.Create(ctx, &globex)
		},
		func(ctx context.Context, sess liteorm.Session) error {
			r := orm.NewRepo[Member](sess)
			// Later steps read the ids the first step generated — that is the
			// whole "reference resolution" mechanism: ordinary Go variables.
			for _, m := range []*Member{
				{OrgID: acme.ID, Name: "Ada"},
				{OrgID: acme.ID, Name: "Grace"},
				{OrgID: globex.ID, Name: "Hank"},
			} {
				if err := r.Create(ctx, m); err != nil {
					return err
				}
			}
			return nil
		},
		func(ctx context.Context, sess liteorm.Session) error {
			r := orm.NewRepo[Project](sess)
			for _, p := range []*Project{
				{OrgID: acme.ID, Title: "Engine"},
				{OrgID: globex.ID, Title: "Doomsday"},
			} {
				if err := r.Create(ctx, p); err != nil {
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("seeded orgs Acme(#%d) and Globex(#%d) with members + projects\n", acme.ID, globex.ID)

	// ---- Multi-tenant reads: each org sees only its own rows ----
	section("Tenant-scoped reads (explicit scope, no hidden filter)")
	members := orm.NewRepo[Member](db)
	projects := orm.NewRepo[Project](db)
	acmeMembers, _ := members.Scopes(membersOfOrg(acme.ID)).OrderBy("name ASC").Find(ctx)
	globexMembers, _ := members.Scopes(membersOfOrg(globex.ID)).Find(ctx)
	fmt.Printf("Acme members: %v\n", memberNames(acmeMembers))
	fmt.Printf("Globex members: %v\n", memberNames(globexMembers))
	ap, _ := projects.Scopes(projectsOfOrg(acme.ID)).Count(ctx)
	gp, _ := projects.Scopes(projectsOfOrg(globex.ID)).Count(ctx)
	fmt.Printf("project counts — Acme: %d  Globex: %d\n", ap, gp)

	// ---- Tenant-stamped writes: the org comes from context ----
	section("Tenant-stamped write (org from context)")
	ctxAcme := withOrg(ctx, acme.ID)
	newcomer := &Member{Name: "Linus"} // OrgID left zero on purpose
	if err := members.Create(ctxAcme, newcomer); err != nil {
		return err
	}
	fmt.Printf("created %q — BeforeCreate stamped org_id=%d from context\n", newcomer.Name, newcomer.OrgID)

	// ---- Atomicity: a failing fixture rolls everything back ----
	section("Atomic fixtures (a failing step rolls the set back)")
	before, _ := orm.NewRepo[Org](db).Count(ctx)
	err = seed(ctx, db,
		func(ctx context.Context, sess liteorm.Session) error {
			return orm.NewRepo[Org](sess).Create(ctx, &Org{Name: "Stark"})
		},
		func(ctx context.Context, sess liteorm.Session) error {
			return fmt.Errorf("simulated failure after the first insert")
		},
	)
	after, _ := orm.NewRepo[Org](db).Count(ctx)
	fmt.Printf("seed returned error: %v\n", err != nil)
	fmt.Printf("org count unchanged by the failed fixture: %d → %d\n", before, after)

	fmt.Println()
	return nil
}

func memberNames(ms []Member) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}
