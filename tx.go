package liteorm

import (
	"context"
	"time"
)

// Transaction runs fn inside a transaction: it begins one on db, calls fn with it, and
// commits when fn returns nil or rolls back when fn returns an error. If fn
// panics, the transaction is rolled back and the panic re-raised. It is the
// closure-style transaction helper — the manual Begin/Commit/Rollback dance done
// correctly once, so the common case needs neither a deferred rollback nor an
// explicit commit.
//
//	err := liteorm.Transaction(ctx, db, func(tx *liteorm.BoundTx) error {
//		if err := orm.NewRepo[Account](tx).Update(ctx, &from); err != nil {
//			return err // rolls back
//		}
//		return orm.NewRepo[Account](tx).Update(ctx, &to)
//	})
func Transaction(ctx context.Context, db *DB, fn func(tx *BoundTx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// RetryPolicy configures TransactionRetry. Max is the maximum number of attempts (a value
// <= 0 is treated as 1, a single attempt). Backoff, when set, returns how long to
// wait before the next attempt, given the 1-based attempt number that just
// failed; a nil Backoff retries immediately.
type RetryPolicy struct {
	Max     int
	Backoff func(attempt int) time.Duration
}

// TransactionRetry runs fn in a transaction like Transaction and retries the WHOLE transaction when
// it fails with a transient, retryable error — a deadlock or serialization
// failure (IsRetryable), normalized identically across every backend, so the same
// retry loop is correct on SQLite, Postgres, MySQL, and SQL Server. Each attempt
// begins a fresh transaction, so fn must be safe to run more than once. A
// non-retryable error, or success, returns immediately; the last error is
// returned once attempts are exhausted. A cancelled context stops the wait
// between attempts.
func TransactionRetry(ctx context.Context, db *DB, p RetryPolicy, fn func(tx *BoundTx) error) error {
	attempts := p.Max
	if attempts <= 0 {
		attempts = 1
	}
	var err error
	for attempt := 1; ; attempt++ {
		err = Transaction(ctx, db, fn)
		if err == nil || !IsRetryable(err) || attempt >= attempts {
			return err
		}
		if p.Backoff != nil {
			timer := time.NewTimer(p.Backoff(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}
