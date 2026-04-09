package liteorm

import (
	"database/sql"
	"errors"
)

// Typed sentinels for normalized constraint/transaction errors. Callers test
// with errors.Is(err, liteorm.ErrUniqueViolation); each backend's normalizeError
// dual-wraps the sentinel and the original driver error so both errors.Is and
// errors.As stay reachable.
var (
	ErrUniqueViolation = errors.New("liteorm: unique constraint violation")
	ErrForeignKey      = errors.New("liteorm: foreign key constraint violation")
	ErrNotNull         = errors.New("liteorm: not-null constraint violation")
	ErrCheck           = errors.New("liteorm: check constraint violation")
	ErrDeadlock        = errors.New("liteorm: deadlock detected")
	ErrSerialization   = errors.New("liteorm: serialization failure")
)

// ErrNoRows is returned by single-row reads that find nothing. It IS
// database/sql.ErrNoRows so errors.Is works uniformly across backends (pgx
// already proxies the same sentinel — see the R3 research).
var ErrNoRows = sql.ErrNoRows

// Classification helpers over the sentinels above — small conveniences so callers
// can write `if liteorm.IsUniqueViolation(err)` instead of spelling out
// errors.Is(err, liteorm.ErrUniqueViolation). Each matches anywhere in the error
// chain (the normalized error dual-wraps both sentinel and driver error).

// IsUniqueViolation reports whether err is a unique/primary-key constraint violation.
func IsUniqueViolation(err error) bool { return errors.Is(err, ErrUniqueViolation) }

// IsForeignKeyViolation reports whether err is a foreign-key constraint violation.
func IsForeignKeyViolation(err error) bool { return errors.Is(err, ErrForeignKey) }

// IsNotNullViolation reports whether err is a NOT NULL constraint violation.
func IsNotNullViolation(err error) bool { return errors.Is(err, ErrNotNull) }

// IsCheckViolation reports whether err is a CHECK constraint violation.
func IsCheckViolation(err error) bool { return errors.Is(err, ErrCheck) }

// IsNotFound reports whether err is the no-rows sentinel (a single-row read that
// matched nothing).
func IsNotFound(err error) bool { return errors.Is(err, ErrNoRows) }

// IsRetryable reports whether err is a transient transaction failure (deadlock or
// serialization) that is safe to retry after rolling back.
func IsRetryable(err error) bool {
	return errors.Is(err, ErrDeadlock) || errors.Is(err, ErrSerialization)
}
