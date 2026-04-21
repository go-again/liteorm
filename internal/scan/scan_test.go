package scan

import (
	"reflect"
	"testing"
	"time"
)

type user struct {
	ID        int64 `db:"id"`
	Name      string
	Email     string
	CreatedAt time.Time
	ignored   string //nolint:unused
	Skip      string `db:"-"`
}

func TestToSnake(t *testing.T) {
	cases := map[string]string{"UserID": "user_id", "CreatedAt": "created_at", "ID": "id", "Name": "name"}
	for in, want := range cases {
		if got := toSnake(in); got != want {
			t.Errorf("toSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanColumnsAndPK(t *testing.T) {
	all := Columns[user](false)
	wantAll := []string{"id", "name", "email", "created_at"}
	if !reflect.DeepEqual(all, wantAll) {
		t.Errorf("Columns(false) = %v, want %v", all, wantAll)
	}
	ins := Columns[user](true) // skip auto-increment PK
	wantIns := []string{"name", "email", "created_at"}
	if !reflect.DeepEqual(ins, wantIns) {
		t.Errorf("Columns(true) = %v, want %v", ins, wantIns)
	}
	pk, ok := PrimaryKey[user]()
	if !ok || pk != "id" {
		t.Errorf("PrimaryKey = %q,%v, want id,true", pk, ok)
	}
}

type gormModel struct {
	ID      int64   `gorm:"column:id;primaryKey;autoIncrement"`
	Email   string  `gorm:"column:email_addr"`
	Ignore  string  `gorm:"-"`
	Orders  []order // relation — must be skipped as a column
	Profile *order  // relation — must be skipped
}

type order struct {
	ID int64
}

func TestResolveColumnAndRelationSkip(t *testing.T) {
	all := Columns[gormModel](false)
	want := []string{"id", "email_addr"} // Ignore skipped, Orders/Profile are relations
	if !reflect.DeepEqual(all, want) {
		t.Errorf("Columns = %v, want %v", all, want)
	}
	if pk, ok := PrimaryKey[gormModel](); !ok || pk != "id" {
		t.Errorf("PrimaryKey = %q,%v", pk, ok)
	}
	ins := Columns[gormModel](true) // skip auto pk
	if !reflect.DeepEqual(ins, []string{"email_addr"}) {
		t.Errorf("Columns(skipAuto) = %v", ins)
	}
}

func TestValues(t *testing.T) {
	u := user{ID: 7, Name: "alice", Email: "a@x.io"}
	got := Values(&u, []string{"name", "email"})
	if len(got) != 2 || got[0] != "alice" || got[1] != "a@x.io" {
		t.Errorf("Values = %v", got)
	}
}
