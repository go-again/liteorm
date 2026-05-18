package gen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestPortGormContent(t *testing.T) {
	cases := []struct {
		field, gorm string
		soft        bool
		want        string
	}{
		{"Email", "column:email;unique", false, "email,unique"},
		{"ID", "primaryKey;autoIncrement", false, "id,pk,autoincrement"},
		{"Name", "not null;size:255;default:anon", false, "name,notnull,size:255,default:anon"},
		{"Bio", "->:false", false, "bio,readonly"},
		{"Secret", "<-:false", false, "secret,writeonly"},
		{"Roles", "many2many:user_roles", false, "roles,m2m:user_roles"},
		{"Company", "foreignKey:CompanyID;references:ID", false, "company,fk:CompanyID,references:ID"},
		{"Slug", "uniqueIndex;index:idx_slug", false, "slug,unique,index:idx_slug"},
		{"Ignore", "-", false, "-"},
		{"DeletedAt", "", true, "deleted_at,soft_delete"},
		// a field whose gorm tag reduces to just the default column → no tag.
		{"Title", "column:title", false, "title"},
	}
	for _, c := range cases {
		got, _ := portGormContent(c.field, c.gorm, c.soft)
		if got != c.want {
			t.Errorf("portGormContent(%q, %q, %v) = %q, want %q", c.field, c.gorm, c.soft, got, c.want)
		}
	}
}

func TestPortGormContentNotes(t *testing.T) {
	_, notes := portGormContent("X", "column:x;serializer:json;comment:hi", false)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes for unsupported keys, got %v", notes)
	}
}

const gormSource = `package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        uint   ` + "`gorm:\"primaryKey\"`" + `
	Email     string ` + "`gorm:\"column:email;unique;not null\" json:\"email\"`" + `
	Name      string ` + "`gorm:\"size:255\"`" + `
	CreatedAt time.Time
	DeletedAt gorm.DeletedAt ` + "`gorm:\"index\"`" + `
	Roles     []Role ` + "`gorm:\"many2many:user_roles\"`" + `
}
`

func TestPortSource(t *testing.T) {
	out, notes, err := PortSource([]byte(gormSource))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	// The rewritten source must still be valid Go.
	if _, perr := parser.ParseFile(token.NewFileSet(), "", out, parser.ParseComments); perr != nil {
		t.Fatalf("ported source is not valid Go: %v\n%s", perr, s)
	}

	wants := []string{
		"`orm:\"id,pk\"`", // uint id → pk (autoincrement is implicit for int pk)
		"`orm:\"email,unique,notnull\" json:\"email\"`", // gorm→orm, json preserved
		"`orm:\"name,size:255\"`",
		"DeletedAt sql.NullTime",               // type rewritten
		"orm:\"deleted_at,index,soft_delete\"", // tag gains soft_delete
		"`orm:\"roles,m2m:user_roles\"`",
	}
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("ported source missing %q\n--- got ---\n%s", w, s)
		}
	}
	// gorm tags should be gone.
	if strings.Contains(s, "gorm:") {
		t.Errorf("ported source still contains a gorm tag:\n%s", s)
	}
	// a note about the DeletedAt type rewrite.
	foundNote := false
	for _, n := range notes {
		if strings.Contains(n.Message, "gorm.DeletedAt") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Errorf("expected a note about gorm.DeletedAt, got %+v", notes)
	}
}
