package orm

import (
	"testing"
	"time"
)

type embeddedAddr struct {
	Street string
	City   string
}

type tagged struct {
	ID        int64
	Name      string `orm:"name,size:100"`
	Age       int    `orm:"age,check:age >= 0"`
	Secret    string `orm:"secret,writeonly"`
	Computed  string `orm:"computed,readonly"`
	CreatedAt time.Time
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	Made      time.Time    `orm:"made,autocreatetime"`
	Addr      embeddedAddr `orm:"embedded,prefix:addr_"`
}

func TestTagGrammar(t *testing.T) {
	s, err := SchemaOf[tagged]()
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string]*Field{}
	for _, f := range s.Fields {
		cols[f.Column] = f
	}
	want := []string{"id", "name", "age", "secret", "computed", "created_at", "updated_at", "made", "addr_street", "addr_city"}
	for _, c := range want {
		if cols[c] == nil {
			t.Fatalf("missing column %q (have %v)", c, keys(cols))
		}
	}
	if cols["name"].Size != 100 {
		t.Errorf("name size = %d, want 100", cols["name"].Size)
	}
	if cols["age"].Check != "age >= 0" {
		t.Errorf("age check = %q", cols["age"].Check)
	}
	if cols["secret"].Readable || !cols["secret"].Writable {
		t.Errorf("secret perms readable=%v writable=%v, want false,true", cols["secret"].Readable, cols["secret"].Writable)
	}
	if !cols["computed"].Readable || cols["computed"].Writable {
		t.Errorf("computed perms readable=%v writable=%v, want true,false", cols["computed"].Readable, cols["computed"].Writable)
	}
	if !cols["made"].AutoCreate {
		t.Error("made should be autoCreate")
	}
	if !cols["updated_at"].AutoUpdate {
		t.Error("updated_at should be autoUpdate")
	}
	// WriteColumns(false) excludes auto-PK id and read-only computed, includes secret.
	wc := map[string]bool{}
	for _, c := range s.WriteColumns(false) {
		wc[c] = true
	}
	if wc["id"] || wc["computed"] || !wc["secret"] {
		t.Errorf("WriteColumns(insert) = %v", s.WriteColumns(false))
	}
}

func keys(m map[string]*Field) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
