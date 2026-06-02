package ormsuite

import (
	"context"
	"testing"

	"liteorm.org/orm"
)

// TestPolymorphicAssociation exercises a toys table owned by both User and Pet
// through (owner_id, owner_type): each owner sees only its own toys, and the
// Assoc handle stamps and clears both columns.
func TestPolymorphicAssociation(t *testing.T) {
	ctx := context.Background()
	toys := orm.NewRepo[Toy](DB)

	u := seedUser(t, "poly-user", Config{})
	pet := &Pet{UserID: u.ID, Name: "poly-pet"}
	mustCreate(t, pet)

	ball := &Toy{Name: "ball"}
	bone := &Toy{Name: "bone"}
	mouse := &Toy{Name: "mouse"}
	for _, toy := range []*Toy{ball, bone, mouse} {
		mustCreate(t, toy)
	}

	// Append stamps owner_id + owner_type for each owner kind.
	userToys, err := orm.Assoc[User, Toy](DB, "Toys", u)
	if err != nil {
		t.Fatal(err)
	}
	if err := userToys.Append(ctx, ball, bone); err != nil {
		t.Fatalf("append to user: %v", err)
	}
	petToys, err := orm.Assoc[Pet, Toy](DB, "Toys", pet)
	if err != nil {
		t.Fatal(err)
	}
	if err := petToys.Append(ctx, mouse); err != nil {
		t.Fatalf("append to pet: %v", err)
	}

	// In-memory: Append set owner_type on the targets.
	if ball.OwnerType != "users" || mouse.OwnerType != "pets" {
		t.Errorf("owner_type not stamped in memory: ball=%q mouse=%q", ball.OwnerType, mouse.OwnerType)
	}

	// Count is type-scoped.
	if n, _ := userToys.Count(ctx); n != 2 {
		t.Errorf("user toy count = %d, want 2", n)
	}
	if n, _ := petToys.Count(ctx); n != 1 {
		t.Errorf("pet toy count = %d, want 1", n)
	}

	// Eager load: a user loads only its toys, never the pet's (cross-owner isolation).
	users := []User{*u}
	if err := orm.Load[User, Toy](ctx, DB, users, "Toys"); err != nil {
		t.Fatalf("load user toys: %v", err)
	}
	if got := len(users[0].Toys); got != 2 {
		t.Fatalf("loaded %d user toys, want 2", got)
	}
	for _, toy := range users[0].Toys {
		if toy.OwnerType != "users" {
			t.Errorf("user toy %q has owner_type %q", toy.Name, toy.OwnerType)
		}
		if toy.Name == "mouse" {
			t.Error("pet's toy leaked into the user's load")
		}
	}

	pets := []Pet{*pet}
	if err := orm.Load[Pet, Toy](ctx, DB, pets, "Toys"); err != nil {
		t.Fatalf("load pet toys: %v", err)
	}
	if got := len(pets[0].Toys); got != 1 || pets[0].Toys[0].Name != "mouse" {
		t.Errorf("pet toys = %+v, want [mouse]", pets[0].Toys)
	}

	// Delete unlinks one toy (nulls owner_id + owner_type) without deleting the row.
	if err := userToys.Delete(ctx, bone); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, _ := userToys.Count(ctx); n != 1 {
		t.Errorf("after delete, user toy count = %d, want 1", n)
	}
	stillThere, err := toys.Get(ctx, bone.ID)
	if err != nil {
		t.Fatalf("detached toy row was deleted: %v", err)
	}
	if stillThere.OwnerID.Valid || stillThere.OwnerType != "" {
		t.Errorf("detach should null both columns, got id=%v type=%q", stillThere.OwnerID, stillThere.OwnerType)
	}

	// Clear unlinks the rest for this owner only.
	if err := userToys.Clear(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ := userToys.Count(ctx); n != 0 {
		t.Errorf("after clear, user toy count = %d, want 0", n)
	}
	if n, _ := petToys.Count(ctx); n != 1 {
		t.Errorf("clear on the user must not touch the pet's toys, pet count = %d", n)
	}
}
