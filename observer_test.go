package liteorm

import (
	"context"
	"errors"
	"testing"

	"liteorm.org/internal/sqlgen"
)

type fakeResult struct{ n int64 }

func (f fakeResult) RowsAffected() int64 { return f.n }

// recConn is a Querier+Beginner whose Exec returns a configurable Result/error
// and that records commits/rollbacks of transactions started from it.
type recConn struct {
	rows           int64
	err            error
	committed      int
	rolledback     int
	commitFailsFor int // the first N commits return ErrSerialization (for retry tests)
}

func (c *recConn) QueryContext(context.Context, string, ...any) (Rows, error) { return nil, c.err }
func (c *recConn) ExecContext(context.Context, string, ...any) (Result, error) {
	return fakeResult{n: c.rows}, c.err
}
func (c *recConn) Begin(ctx context.Context) (Tx, error) { return c.BeginTx(ctx, TxOptions{}) }
func (c *recConn) BeginTx(context.Context, TxOptions) (Tx, error) {
	return recTx{c}, nil
}

type recTx struct{ c *recConn }

func (t recTx) QueryContext(context.Context, string, ...any) (Rows, error) { return nil, t.c.err }
func (t recTx) ExecContext(context.Context, string, ...any) (Result, error) {
	return fakeResult{n: t.c.rows}, t.c.err
}
func (t recTx) Begin(context.Context) (Tx, error) { return t, nil }
func (t recTx) Commit(context.Context) error {
	t.c.committed++
	if t.c.committed <= t.c.commitFailsFor {
		return ErrSerialization // a serialization failure surfaced at COMMIT
	}
	return nil
}
func (t recTx) Rollback(context.Context) error { t.c.rolledback++; return nil }

type obsKey string

// recObserver records the events it sees and tags the context, so a test can
// assert ordering and context propagation across Before/After.
type recObserver struct {
	name  string
	seen  *[]string
	befor []*QueryEvent
	aftr  []*QueryEvent
}

func (o *recObserver) BeforeQuery(ctx context.Context, ev *QueryEvent) context.Context {
	o.befor = append(o.befor, ev)
	*o.seen = append(*o.seen, o.name+":before")
	return context.WithValue(ctx, obsKey(o.name), true)
}

func (o *recObserver) AfterQuery(ctx context.Context, ev *QueryEvent) {
	o.aftr = append(o.aftr, ev)
	*o.seen = append(*o.seen, o.name+":after")
	if ctx.Value(obsKey(o.name)) != true {
		*o.seen = append(*o.seen, o.name+":CTX-LOST")
	}
}

func TestObserver_ExecCapturesEvent(t *testing.T) {
	var seen []string
	o := &recObserver{name: "o", seen: &seen}
	db := New(&recConn{rows: 7}, sqlgen.SQLite, WithObserver(o))

	res, err := db.ExecContext(context.Background(), "UPDATE t SET x=?", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected() != 7 {
		t.Fatalf("rows = %d", res.RowsAffected())
	}
	if len(o.aftr) != 1 {
		t.Fatalf("AfterQuery fired %d times, want 1", len(o.aftr))
	}
	ev := o.aftr[0]
	if ev.Op != MsgExec || ev.SQL != "UPDATE t SET x=?" || len(ev.Args) != 1 || ev.Rows != 7 || ev.Err != nil {
		t.Fatalf("event = %+v", ev)
	}
}

func TestObserver_QueryEventRowsUnset(t *testing.T) {
	var seen []string
	o := &recObserver{name: "o", seen: &seen}
	db := New(&recConn{}, sqlgen.SQLite, WithObserver(o))
	if _, err := db.QueryContext(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if o.aftr[0].Op != MsgQuery || o.aftr[0].Rows != -1 {
		t.Fatalf("query event = %+v; want Op=query Rows=-1", o.aftr[0])
	}
}

func TestObserver_NestingLIFOAndCtx(t *testing.T) {
	var seen []string
	o1 := &recObserver{name: "o1", seen: &seen}
	o2 := &recObserver{name: "o2", seen: &seen}
	db := New(&recConn{rows: 1}, sqlgen.SQLite, WithObserver(o1), WithObserver(o2))

	if _, err := db.ExecContext(context.Background(), "DELETE FROM t"); err != nil {
		t.Fatal(err)
	}
	want := []string{"o1:before", "o2:before", "o2:after", "o1:after"}
	if len(seen) != len(want) {
		t.Fatalf("event order = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("event order = %v, want %v", seen, want)
		}
	}
}

func TestObserver_InsideTransaction(t *testing.T) {
	var seen []string
	o := &recObserver{name: "tx", seen: &seen}
	db := New(&recConn{rows: 2}, sqlgen.SQLite, WithObserver(o))
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if len(o.aftr) != 1 || o.aftr[0].Rows != 2 {
		t.Fatalf("observer did not fire for a tx statement: %+v", o.aftr)
	}
}

func TestObserver_ErrorPropagated(t *testing.T) {
	var seen []string
	o := &recObserver{name: "o", seen: &seen}
	boom := errors.New("boom")
	db := New(&recConn{err: boom}, sqlgen.SQLite, WithObserver(o))
	if _, err := db.ExecContext(context.Background(), "BAD"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !errors.Is(o.aftr[0].Err, boom) {
		t.Fatalf("event.Err = %v, want boom", o.aftr[0].Err)
	}
}

func TestObserver_NoneIsFastPath(t *testing.T) {
	// With no observer and logging off, statements still execute (the fast path
	// must not drop the call).
	db := New(&recConn{rows: 5}, sqlgen.SQLite)
	res, err := db.ExecContext(context.Background(), "UPDATE t SET x=1")
	if err != nil || res.RowsAffected() != 5 {
		t.Fatalf("fast path broke exec: res=%v err=%v", res, err)
	}
}
