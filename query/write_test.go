package query

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

func TestUpdateBuilderRender(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	up, err := Update[tuser](sess).Set("name", "x").Set("age", 5).Where("age < ?", 18).resolved()
	if err != nil {
		t.Fatal(err)
	}
	q, args, err := up.Build(sqlgen.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if q != `UPDATE "users" SET "name" = ?, "age" = ? WHERE age < ?` {
		t.Errorf("update render: %q", q)
	}
	if len(args) != 3 || args[0] != "x" || args[1] != 5 || args[2] != 18 {
		t.Errorf("args = %v, want [x 5 18]", args)
	}
}

func TestUpdateSetExprAndFrom(t *testing.T) {
	// SetExpr without From: a self-referential increment.
	up, _ := Update[tuser](mockSession{d: sqlgen.SQLite}).SetExpr("age", "age + ?", 1).Where("name = ?", "x").resolved()
	q, args, _ := up.Build(sqlgen.SQLite)
	if q != `UPDATE "users" SET "age" = age + ? WHERE name = ?` || len(args) != 2 || args[0] != 1 || args[1] != "x" {
		t.Errorf("setexpr render: %q args=%v", q, args)
	}

	// UPDATE … FROM renders on the dialects that support it.
	for _, d := range []dialect.Dialect{sqlgen.SQLite, sqlgen.Postgres, sqlgen.MSSQL} {
		up, err := Update[tuser](mockSession{d: d}).
			SetExpr("name", "s.name").From("src AS s").Where("users.id = s.id").resolved()
		if err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		q, _, _ := up.Build(d)
		if !strings.Contains(q, " FROM src AS s WHERE ") {
			t.Errorf("%s: UPDATE…FROM render: %q", d.Name(), q)
		}
	}
	// MySQL doesn't support UPDATE … FROM — a clear build error.
	if _, err := Update[tuser](mockSession{d: sqlgen.MySQL}).
		SetExpr("name", "s.name").From("src s").Where("x = y").resolved(); err == nil {
		t.Error("UPDATE…FROM on MySQL should error")
	}
}

func TestUpdateReturningOutputOrder(t *testing.T) {
	// On MSSQL the OUTPUT clause sits after SET and before FROM.
	up, _ := Update[tuser](mockSession{d: sqlgen.MSSQL}).
		SetExpr("name", "s.name").From("src AS s").Where("users.id = s.id").resolved()
	up.Returning = []string{"id", "name"}
	q, _, _ := up.Build(sqlgen.MSSQL)
	if !strings.Contains(q, "OUTPUT INSERTED.[id], INSERTED.[name] FROM src AS s WHERE") {
		t.Errorf("MSSQL OUTPUT/FROM order: %q", q)
	}
}

func TestWriteWhereGuard(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	if _, err := Update[tuser](sess).Set("name", "x").resolved(); err == nil {
		t.Error("a WHERE-less UPDATE should be refused")
	}
	if _, err := Delete[tuser](sess).resolved(); err == nil {
		t.Error("a WHERE-less DELETE should be refused")
	}
	// An explicit tautology opts in to affecting every row.
	if _, err := Update[tuser](sess).Set("name", "x").Where("1 = 1").resolved(); err != nil {
		t.Errorf("Where(\"1 = 1\") should be allowed: %v", err)
	}
}

func TestUpdateUnknownColumn(t *testing.T) {
	if _, err := Update[tuser](mockSession{d: sqlgen.SQLite}).Set("nope", 1).Where("1=1").resolved(); err == nil {
		t.Error("Set on an unknown column should error")
	}
}

func TestDeleteBuilderRender(t *testing.T) {
	del, err := Delete[tuser](mockSession{d: sqlgen.SQLite}).Filter(Col[int]("age").Lt(18)).resolved()
	if err != nil {
		t.Fatal(err)
	}
	q, args, _ := del.Build(sqlgen.SQLite)
	if q != `DELETE FROM "users" WHERE "age" < ?` || len(args) != 1 || args[0] != 18 {
		t.Errorf("delete render: %q args=%v", q, args)
	}
}
