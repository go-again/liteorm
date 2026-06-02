package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

// fresh re-reads a user by id with all association fields cleared, so a Load
// genuinely populates them (rather than inspecting what seedUser left in memory).
func fresh(t *testing.T, id int64) []User {
	t.Helper()
	u, err := orm.NewRepo[User](DB).Get(context.Background(), id)
	if err != nil {
		t.Fatalf("re-read user %d: %v", id, err)
	}
	return []User{u}
}

func TestBelongsTo(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "bt", Config{Company: true})
	list := fresh(t, u.ID)
	if err := orm.Load[User, Company](ctx, DB, list, "Company"); err != nil {
		t.Fatal(err)
	}
	if list[0].Company == nil || list[0].Company.Name != "company-bt" {
		t.Errorf("belongs-to Company = %+v", list[0].Company)
	}
}

func TestHasOne(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "ho", Config{Account: true})
	list := fresh(t, u.ID)
	if err := orm.Load[User, Account](ctx, DB, list, "Account"); err != nil {
		t.Fatal(err)
	}
	if list[0].Account == nil || list[0].Account.Number != "ho_account" {
		t.Errorf("has-one Account = %+v", list[0].Account)
	}
}

func TestHasMany(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "hm", Config{Pets: 3})
	list := fresh(t, u.ID)
	if err := orm.Load[User, Pet](ctx, DB, list, "Pets"); err != nil {
		t.Fatal(err)
	}
	if len(list[0].Pets) != 3 {
		t.Errorf("has-many Pets = %d, want 3", len(list[0].Pets))
	}
}

func TestSelfReferentialTeam(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "team", Config{Team: 2})
	list := fresh(t, u.ID)
	if err := orm.Load[User, User](ctx, DB, list, "Team"); err != nil {
		t.Fatal(err)
	}
	if len(list[0].Team) != 2 {
		t.Errorf("self-referential Team = %d, want 2", len(list[0].Team))
	}
}

func TestManyToMany(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "m2m", Config{Languages: 2})
	list := fresh(t, u.ID)
	if err := orm.Load[User, Language](ctx, DB, list, "Languages"); err != nil {
		t.Fatal(err)
	}
	if len(list[0].Languages) != 2 {
		t.Errorf("many-to-many Languages = %d, want 2", len(list[0].Languages))
	}
}

func TestAssociationWrites(t *testing.T) {
	ctx := context.Background()
	u := seedUser(t, "assoc-w", Config{})

	// has-many: link existing pets to the user, then detach one.
	p1, p2 := &Pet{Name: "aw-p1"}, &Pet{Name: "aw-p2"}
	mustCreate(t, p1)
	mustCreate(t, p2)
	pets, err := orm.Assoc[User, Pet](DB, "Pets", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := pets.Append(ctx, p1, p2); err != nil {
		t.Fatal(err)
	}
	if n, _ := pets.Count(ctx); n != 2 {
		t.Errorf("has-many Count after Append = %d, want 2", n)
	}
	if err := pets.Delete(ctx, p1); err != nil {
		t.Fatal(err)
	}
	if n, _ := pets.Count(ctx); n != 1 {
		t.Errorf("has-many Count after Delete = %d, want 1", n)
	}

	// many-to-many: append/replace/clear language links.
	en, fr := &Language{Code: "aw-en", Name: "en"}, &Language{Code: "aw-fr", Name: "fr"}
	mustCreate(t, en)
	mustCreate(t, fr)
	langs, err := orm.Assoc[User, Language](DB, "Languages", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := langs.Append(ctx, en); err != nil {
		t.Fatal(err)
	}
	if err := langs.Replace(ctx, fr); err != nil { // set becomes exactly {fr}
		t.Fatal(err)
	}
	if n, _ := langs.Count(ctx); n != 1 {
		t.Errorf("m2m Count after Replace = %d, want 1", n)
	}
	if err := langs.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := langs.Count(ctx); n != 0 {
		t.Errorf("m2m Count after Clear = %d, want 0", n)
	}
}
