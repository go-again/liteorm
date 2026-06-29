package liteorm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"liteorm.org/internal/sqlgen"
)

func TestTx_Commit(t *testing.T) {
	fc := &recConn{}
	db := New(fc, sqlgen.SQLite)
	if err := Transaction(context.Background(), db, func(*BoundTx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fc.committed != 1 || fc.rolledback != 0 {
		t.Fatalf("committed=%d rolledback=%d, want 1/0", fc.committed, fc.rolledback)
	}
}

func TestTx_RollbackOnError(t *testing.T) {
	fc := &recConn{}
	db := New(fc, sqlgen.SQLite)
	boom := errors.New("boom")
	if err := Transaction(context.Background(), db, func(*BoundTx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if fc.committed != 0 || fc.rolledback != 1 {
		t.Fatalf("committed=%d rolledback=%d, want 0/1", fc.committed, fc.rolledback)
	}
}

func TestTx_RollbackOnPanic(t *testing.T) {
	fc := &recConn{}
	db := New(fc, sqlgen.SQLite)
	defer func() {
		if recover() == nil {
			t.Fatal("Tx must re-panic")
		}
		if fc.rolledback != 1 {
			t.Fatalf("panic must roll back: rolledback=%d", fc.rolledback)
		}
	}()
	_ = Transaction(context.Background(), db, func(*BoundTx) error { panic("kaboom") })
}

func TestTxRetry_RetriesUntilSuccess(t *testing.T) {
	fc := &recConn{}
	db := New(fc, sqlgen.SQLite)
	calls := 0
	err := TransactionRetry(context.Background(), db, RetryPolicy{Max: 5}, func(*BoundTx) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("transient: %w", ErrSerialization)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("fn called %d times, want 3", calls)
	}
	if fc.committed != 1 || fc.rolledback != 2 {
		t.Fatalf("committed=%d rolledback=%d, want 1/2", fc.committed, fc.rolledback)
	}
}

func TestTxRetry_GivesUpAfterMax(t *testing.T) {
	db := New(&recConn{}, sqlgen.SQLite)
	calls := 0
	err := TransactionRetry(context.Background(), db, RetryPolicy{Max: 2}, func(*BoundTx) error {
		calls++
		return fmt.Errorf("deadlock: %w", ErrDeadlock)
	})
	if !IsRetryable(err) {
		t.Fatalf("err = %v, want a retryable error after giving up", err)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2", calls)
	}
}

func TestTxRetry_RetriesCommitFailure(t *testing.T) {
	// A serialization failure surfaced at COMMIT (the realistic case on
	// Postgres/MySQL) is retryable too: the first commit fails, the retry commits.
	fc := &recConn{commitFailsFor: 1}
	db := New(fc, sqlgen.SQLite)
	calls := 0
	err := TransactionRetry(context.Background(), db, RetryPolicy{Max: 3}, func(*BoundTx) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil after retrying a commit-time serialization failure", err)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2 (one commit failed, one succeeded)", calls)
	}
}

func TestTxRetry_NonRetryableStopsImmediately(t *testing.T) {
	db := New(&recConn{}, sqlgen.SQLite)
	boom := errors.New("boom")
	calls := 0
	err := TransactionRetry(context.Background(), db, RetryPolicy{Max: 5}, func(*BoundTx) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want boom after 1 call", err, calls)
	}
}
