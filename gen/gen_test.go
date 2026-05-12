package gen

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

type genOrder struct{ ID int64 }

type genUser struct {
	ID        int64
	Name      string
	Email     string `db:"email_addr"`
	Active    bool
	CreatedAt time.Time
	Orders    []genOrder // relation — skipped
	Ignore    string     `db:"-"`
}

func (genUser) TableName() string { return "users" }

func TestFromType(t *testing.T) {
	m := FromType[genUser]()
	if m.GoName != "genUser" || m.Table != "users" {
		t.Fatalf("model = %s/%s", m.GoName, m.Table)
	}
	want := []Field{
		{"ID", "id", "int64"},
		{"Name", "name", "string"},
		{"Email", "email_addr", "string"},
		{"Active", "active", "bool"},
		{"CreatedAt", "created_at", "time.Time"},
	}
	if len(m.Fields) != len(want) {
		t.Fatalf("fields = %+v", m.Fields)
	}
	for i, f := range m.Fields {
		if f != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, f, want[i])
		}
	}
}

func TestWriteColumnsValidAndTyped(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteColumns(&buf, "models", FromType[genUser]()); err != nil {
		t.Fatalf("WriteColumns (also validates the Go is gofmt-parseable): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"package models",
		`"liteorm.org/query"`,
		"genUserColumns = struct {",
		"query.Column[time.Time]",
		`query.Col[string]("email_addr")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated code missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteModelsValid(t *testing.T) {
	m := Model{GoName: "Account", Table: "accounts", Fields: []Field{
		{"ID", "id", "int64"}, {"Name", "name", "string"},
	}}
	var buf bytes.Buffer
	if err := WriteModels(&buf, "models", m); err != nil {
		t.Fatalf("WriteModels: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"type Account struct {", "func (Account) TableName() string", "AccountColumns"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
}

func TestGoName(t *testing.T) {
	cases := map[string]string{"user_id": "UserID", "created_at": "CreatedAt", "users": "Users", "id": "ID", "api_url": "APIURL"}
	for in, want := range cases {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLToGo(t *testing.T) {
	cases := []struct{ sqlType, want string }{
		{"bigint", "int64"}, {"integer", "int64"}, {"character varying", "string"},
		{"text", "string"}, {"timestamp with time zone", "time.Time"}, {"datetime2", "time.Time"},
		{"bit", "bool"}, {"boolean", "bool"}, {"double precision", "float64"}, {"bytea", "[]byte"},
	}
	for _, c := range cases {
		if got := sqlToGo(c.sqlType); got != c.want {
			t.Errorf("sqlToGo(%q) = %q, want %q", c.sqlType, got, c.want)
		}
	}
}
