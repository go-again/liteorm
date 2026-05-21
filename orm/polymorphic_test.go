package orm

import "testing"

type polyOwner struct {
	ID   int64
	Name string
	Toys []polyToy `orm:"polymorphic:Owner"`
}

func (polyOwner) TableName() string { return "poly_owners" }

type polyToy struct {
	ID        int64
	Name      string
	OwnerID   int64  `orm:"owner_id"`
	OwnerType string `orm:"owner_type"`
}

func (polyToy) TableName() string { return "poly_toys" }

func TestPolymorphicInference(t *testing.T) {
	s, err := SchemaOf[polyOwner]()
	if err != nil {
		t.Fatal(err)
	}
	rel, ok := s.Relations["Toys"]
	if !ok {
		t.Fatal("Toys relation not inferred")
	}
	if rel.Kind != RelHasMany {
		t.Errorf("Kind = %v, want RelHasMany", rel.Kind)
	}
	if rel.TargetKey != "owner_id" {
		t.Errorf("TargetKey = %q, want owner_id", rel.TargetKey)
	}
	if rel.PolymorphicType != "owner_type" {
		t.Errorf("PolymorphicType = %q, want owner_type", rel.PolymorphicType)
	}
	if rel.PolymorphicValue != "poly_owners" { // defaults to the owner's table name
		t.Errorf("PolymorphicValue = %q, want poly_owners", rel.PolymorphicValue)
	}
}

// polyBadOwner points at a target missing the owner_type column.
type polyBadOwner struct {
	ID    int64
	Items []polyBadItem `orm:"polymorphic:Owner"`
}

func (polyBadOwner) TableName() string { return "poly_bad_owners" }

type polyBadItem struct {
	ID      int64
	OwnerID int64 `orm:"owner_id"` // no owner_type column
}

func (polyBadItem) TableName() string { return "poly_bad_items" }

func TestPolymorphicMissingColumnErrors(t *testing.T) {
	if _, err := SchemaOf[polyBadOwner](); err == nil {
		t.Fatal("expected a hard error when the target lacks the owner_type column")
	}
}
