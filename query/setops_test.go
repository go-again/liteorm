package query

import (
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

func TestSetOperations(t *testing.T) {
	young := func(d dialect.Dialect) *SelectBuilder[tuser] {
		return Select[tuser](mockSession{d: d}).Filter(Col[int]("age").Lt(18))
	}
	old := func(d dialect.Dialect) *SelectBuilder[tuser] {
		return Select[tuser](mockSession{d: d}).Filter(Col[int]("age").Gt(65))
	}

	// INTERSECT / EXCEPT render on the supporting dialects.
	for _, d := range []dialect.Dialect{sqlgen.SQLite, sqlgen.Postgres, sqlgen.MSSQL} {
		q, _, err := young(d).Intersect(old(d)).buildSQL()
		if err != nil {
			t.Fatalf("%s intersect: %v", d.Name(), err)
		}
		if !strings.Contains(q, " INTERSECT SELECT") {
			t.Errorf("%s: %q missing INTERSECT", d.Name(), q)
		}
	}
	if q, _, _ := young(sqlgen.SQLite).Except(old(sqlgen.SQLite)).buildSQL(); !strings.Contains(q, " EXCEPT SELECT") {
		t.Errorf("except: %q", q)
	}
	if q, _, _ := young(sqlgen.Postgres).ExceptAll(old(sqlgen.Postgres)).buildSQL(); !strings.Contains(q, " EXCEPT ALL SELECT") {
		t.Errorf("except all: %q", q)
	}
	// MySQL doesn't advertise INTERSECT/EXCEPT — a clear build error, not bad SQL.
	if _, _, err := young(sqlgen.MySQL).Intersect(old(sqlgen.MySQL)).buildSQL(); err == nil {
		t.Error("INTERSECT on MySQL should error")
	}
}

func TestRowLocking(t *testing.T) {
	for _, d := range []dialect.Dialect{sqlgen.Postgres, sqlgen.MySQL} {
		q, _, err := Select[tuser](mockSession{d: d}).Filter(Col[int]("age").Gt(1)).ForUpdate().buildSQL()
		if err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		if !strings.HasSuffix(q, " FOR UPDATE") {
			t.Errorf("%s: %q missing FOR UPDATE", d.Name(), q)
		}
	}
	if q, _, _ := Select[tuser](mockSession{d: sqlgen.Postgres}).ForShare().SkipLocked().buildSQL(); !strings.HasSuffix(q, " FOR SHARE SKIP LOCKED") {
		t.Errorf("for share skip locked: %q", q)
	}
	// NoWait implies a FOR UPDATE lock.
	if q, _, _ := Select[tuser](mockSession{d: sqlgen.MySQL}).NoWait().buildSQL(); !strings.HasSuffix(q, " FOR UPDATE NOWAIT") {
		t.Errorf("nowait: %q", q)
	}
	// SQLite and MSSQL have no FOR UPDATE — a clear build error.
	for _, d := range []dialect.Dialect{sqlgen.SQLite, sqlgen.MSSQL} {
		if _, _, err := Select[tuser](mockSession{d: d}).ForUpdate().buildSQL(); err == nil {
			t.Errorf("%s: row locking should error", d.Name())
		}
	}
}

func TestCompoundArmLockRejected(t *testing.T) {
	pg := mockSession{d: sqlgen.Postgres}
	young := func() *SelectBuilder[tuser] { return Select[tuser](pg).Filter(Col[int]("age").Lt(18)) }
	old := func() *SelectBuilder[tuser] { return Select[tuser](pg).Filter(Col[int]("age").Gt(65)) }

	// A lock belongs on the whole compound (the receiver), not an arm — reject it
	// rather than silently drop it.
	if _, _, err := young().Union(old().ForUpdate()).buildSQL(); err == nil {
		t.Error("a row lock on a compound arm should error")
	}
	// The receiver may lock the whole compound.
	if _, _, err := young().ForUpdate().Union(old()).buildSQL(); err != nil {
		t.Errorf("locking the whole compound should be fine: %v", err)
	}
}

func TestScalarAggRejectsGrouping(t *testing.T) {
	sess := mockSession{d: sqlgen.SQLite}
	// A whole-set aggregate can't combine with GROUP BY (use Into instead).
	if _, err := Sum(t.Context(), Select[tuser](sess).GroupByCols(Col[int]("age").Field()), Col[int]("age")); err == nil {
		t.Error("Sum over a GroupBy query should error")
	}
	// A plain whole-set aggregate path doesn't trip the guard (it reaches exec).
	if _, err := Sum(t.Context(), Select[tuser](sess), Col[string]("nope")); err == nil {
		t.Error("Sum on an unknown column should error")
	}
}

func TestDistinctOn(t *testing.T) {
	q, _, err := Select[tuser](mockSession{d: sqlgen.Postgres}).
		DistinctOn(Col[int]("age").Field()).
		Order(Asc(Col[int]("age"))).buildSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q, `SELECT DISTINCT ON ("age") `) {
		t.Errorf("distinct on: %q", q)
	}
	for _, d := range []dialect.Dialect{sqlgen.SQLite, sqlgen.MySQL, sqlgen.MSSQL} {
		if _, _, err := Select[tuser](mockSession{d: d}).DistinctOn(Col[int]("age").Field()).buildSQL(); err == nil {
			t.Errorf("%s: DISTINCT ON should error", d.Name())
		}
	}
}
