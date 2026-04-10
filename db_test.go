package liteorm

import (
	"context"
	"testing"

	"liteorm.org/internal/sqlgen"
)

// fakeConn is a Querier + Beginner that records calls, with no real database.
type fakeConn struct {
	closed bool
	begun  int
}

func (f *fakeConn) QueryContext(context.Context, string, ...any) (Rows, error)  { return nil, nil }
func (f *fakeConn) ExecContext(context.Context, string, ...any) (Result, error) { return nil, nil }
func (f *fakeConn) Begin(ctx context.Context) (Tx, error)                       { return f.BeginTx(ctx, TxOptions{}) }
func (f *fakeConn) BeginTx(context.Context, TxOptions) (Tx, error)              { f.begun++; return fakeTx{}, nil }
func (f *fakeConn) Close() error                                                { f.closed = true; return nil }

type fakeTx struct{}

func (fakeTx) QueryContext(context.Context, string, ...any) (Rows, error)  { return nil, nil }
func (fakeTx) ExecContext(context.Context, string, ...any) (Result, error) { return nil, nil }
func (fakeTx) Begin(context.Context) (Tx, error)                           { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                                { return nil }
func (fakeTx) Rollback(context.Context) error                              { return nil }

// noTxConn is a Querier that is NOT a Beginner.
type noTxConn struct{}

func (noTxConn) QueryContext(context.Context, string, ...any) (Rows, error)  { return nil, nil }
func (noTxConn) ExecContext(context.Context, string, ...any) (Result, error) { return nil, nil }

func TestDBTransactionsAndClose(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{}
	db := New(fc, sqlgen.SQLite)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := tx.Begin(ctx) // nested = savepoint
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if fc.begun != 1 {
		t.Errorf("backend BeginTx called %d times, want 1", fc.begun)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if !fc.closed {
		t.Error("Close must close an io.Closer backend")
	}
}

func TestDBBeginWithoutBeginner(t *testing.T) {
	db := New(noTxConn{}, sqlgen.SQLite)
	if _, err := db.Begin(context.Background()); err == nil {
		t.Error("Begin on a backend that is not a Beginner must error")
	}
	// Close on a non-Closer backend is a no-op, not an error.
	if err := db.Close(); err != nil {
		t.Errorf("Close on a non-Closer backend: %v", err)
	}
}
