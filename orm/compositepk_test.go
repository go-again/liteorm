package orm

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

type membership struct {
	TenantID int64 `orm:"tenant_id,pk"`
	UserID   int64 `orm:"user_id,pk"`
	Role     string
}

func (membership) TableName() string { return "memberships" }

func TestCompositePKSchema(t *testing.T) {
	s, err := SchemaOf[membership]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PKs) != 2 {
		t.Fatalf("PKs = %d, want 2", len(s.PKs))
	}
	if s.PK != nil {
		t.Error("a composite key should leave the single-PK convenience (Schema.PK) nil")
	}
	for _, pk := range s.PKs {
		if pk.Auto {
			t.Errorf("composite PK member %q must not be auto-increment", pk.Column)
		}
	}
}

func TestCompositePKTableDDL(t *testing.T) {
	s, err := SchemaOf[membership]()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `PRIMARY KEY ("tenant_id", "user_id")`},
		{sqlgen.Postgres, `PRIMARY KEY ("tenant_id", "user_id")`},
		{sqlgen.MySQL, "PRIMARY KEY (`tenant_id`, `user_id`)"},
		{sqlgen.MSSQL, `PRIMARY KEY ([tenant_id], [user_id])`},
	}
	for _, c := range cases {
		ddl := createTableSQL(s, c.d, nil)
		if !strings.Contains(ddl, c.want) {
			t.Errorf("%s: missing table-level composite PK:\n%s", c.d.Name(), ddl)
		}
		// The key columns must not also be declared inline PRIMARY KEY.
		if strings.Contains(ddl, "PRIMARY KEY AUTOINCREMENT") {
			t.Errorf("%s: composite key must not auto-increment:\n%s", c.d.Name(), ddl)
		}
	}
}
