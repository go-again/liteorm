package orm

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

type fkCompany struct {
	ID   int64
	Name string
}

func (fkCompany) TableName() string { return "fk_companies" }

type fkUser struct {
	ID        int64
	CompanyID int64      `orm:"company_id"`
	Company   *fkCompany `orm:"fk:company_id"` // belongs-to: FK company_id on fkUser
}

func (fkUser) TableName() string { return "fk_users" }

// fkUserTagged opts a single relation into an FK constraint via the tag.
type fkUserTagged struct {
	ID        int64
	CompanyID int64      `orm:"company_id"`
	Company   *fkCompany `orm:"fk:company_id,constraint:fk"`
}

func (fkUserTagged) TableName() string { return "fk_users" }

func TestForeignKeyEmissionOptIn(t *testing.T) {
	s, err := SchemaOf[fkUser]()
	if err != nil {
		t.Fatal(err)
	}
	// Default (no option, no tag): no FK constraints — current behavior is unchanged.
	if fks := foreignKeysFor(s, migrateConfig{}); len(fks) != 0 {
		t.Errorf("default config emitted %d FKs, want 0", len(fks))
	}
	if ddl := createTableSQL(s, sqlgen.SQLite, nil); strings.Contains(ddl, "FOREIGN KEY") {
		t.Errorf("default DDL must not contain FOREIGN KEY:\n%s", ddl)
	}

	// WithForeignKeys: the belongs-to gets a constraint.
	fks := foreignKeysFor(s, migrateConfig{foreignKeys: true})
	if len(fks) != 1 || fks[0].Column != "company_id" || fks[0].RefTable != "fk_companies" || fks[0].RefColumn != "id" {
		t.Fatalf("foreignKeysFor = %+v", fks)
	}
	cases := []struct {
		d    dialect.Dialect
		want string
	}{
		{sqlgen.SQLite, `FOREIGN KEY ("company_id") REFERENCES "fk_companies" ("id")`},
		{sqlgen.Postgres, `FOREIGN KEY ("company_id") REFERENCES "fk_companies" ("id")`},
		{sqlgen.MySQL, "FOREIGN KEY (`company_id`) REFERENCES `fk_companies` (`id`)"},
		{sqlgen.MSSQL, `FOREIGN KEY ([company_id]) REFERENCES [fk_companies] ([id])`},
	}
	for _, c := range cases {
		ddl := createTableSQL(s, c.d, fks)
		if !strings.Contains(ddl, c.want) {
			t.Errorf("%s: missing FK clause:\n%s", c.d.Name(), ddl)
		}
	}
}

func TestForeignKeyEmissionViaTag(t *testing.T) {
	s, err := SchemaOf[fkUserTagged]()
	if err != nil {
		t.Fatal(err)
	}
	// The tag opts this relation in even without the global option.
	fks := foreignKeysFor(s, migrateConfig{})
	if len(fks) != 1 || fks[0].Column != "company_id" {
		t.Fatalf("constraint:fk tag did not opt in: %+v", fks)
	}
}
