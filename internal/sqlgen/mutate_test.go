package sqlgen_test

import (
	"strings"
	"testing"

	"liteorm.org/internal/sqlgen"
)

func TestUpdateDeleteOutputMSSQL(t *testing.T) {
	up := sqlgen.Update{Table: "t", Set: []sqlgen.SetClause{{Column: "n", Arg: 1}},
		Where: []sqlgen.Expr{{SQL: "id = ?", Args: []any{1}}}, Returning: []string{"id"}}
	q, _, err := up.Build(sqlgen.MSSQL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, "OUTPUT INSERTED.[id]") || strings.Index(q, "OUTPUT") > strings.Index(q, "WHERE") {
		t.Errorf("UPDATE OUTPUT (must precede WHERE): %s", q)
	}
	del := sqlgen.Delete{Table: "t", Where: []sqlgen.Expr{{SQL: "id = ?", Args: []any{1}}}, Returning: []string{"id"}}
	q, _, _ = del.Build(sqlgen.MSSQL)
	if !strings.Contains(q, "OUTPUT DELETED.[id]") || strings.Index(q, "OUTPUT") > strings.Index(q, "WHERE") {
		t.Errorf("DELETE OUTPUT (must precede WHERE): %s", q)
	}
	// pg uses RETURNING at the end.
	q, _, _ = up.Build(sqlgen.Postgres)
	if !strings.HasSuffix(q, `RETURNING "id"`) {
		t.Errorf("UPDATE RETURNING on pg: %s", q)
	}
}

func TestInsertGuards(t *testing.T) {
	if _, _, err := (sqlgen.Insert{Table: "t", Rows: [][]any{{1}}}).Build(sqlgen.SQLite); err == nil {
		t.Error("zero columns should error")
	}
	if _, _, err := (sqlgen.Insert{Table: "t", Columns: []string{"a", "b"}, Rows: [][]any{{1}}}).Build(sqlgen.SQLite); err == nil {
		t.Error("row/column arity mismatch should error")
	}
}
