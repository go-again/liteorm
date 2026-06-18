// Package liteorm is the driver-free core of the liteorm data-access library:
// the lean Querier/Rows/Result/Beginner/Tx contracts, demand-driven capability
// interfaces, normalized error sentinels, and the DB/Session handles the
// front-ends operate against. Backends (e.g. liteorm.org/dialect/sqlite)
// implement these contracts; consumers wire a backend in at construction so the
// core carries no driver dependency. This surface covers both native-driver and
// database/sql backends without leaking driver types.
package liteorm

import (
	"context"
	"log/slog"

	"liteorm.org/dialect"
)

// Querier is the lean core every backend implements. No driver type appears in
// the signature.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
}

// Rows is the driver-neutral row cursor. Scan is the primary, leak-free path;
// Values is advanced/best-effort (on a future pgx backend it may surface driver
// types for exotic columns — see the R3 research).
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Columns() ([]string, error)
	Close() error
	Err() error
	Values() ([]any, error)
}

// Result abstracts pgconn.CommandTag and database/sql.Result. RowsAffected is
// first-class; LastInsertId is the optional LastInsertIder capability.
type Result interface {
	RowsAffected() int64
}

// TxOptions configures a transaction.
type TxOptions struct {
	IsoLevel   string // "serializable" | "repeatable read" | "read committed" | "read uncommitted"
	ReadOnly   bool
	Deferrable bool
}

// Beginner is implemented by backends that support transactions.
type Beginner interface {
	Begin(ctx context.Context) (Tx, error)
	BeginTx(ctx context.Context, opts TxOptions) (Tx, error)
}

// Tx is a transaction. Nested Begin is a savepoint, uniform across backends.
type Tx interface {
	Querier
	Begin(ctx context.Context) (Tx, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Session is a query-executing handle that knows its dialect and logger: both
// *DB and *BoundTx satisfy it, so the front-ends work identically on a
// connection or inside a transaction.
type Session interface {
	Querier
	Dialect() dialect.Dialect
	Logger() *slog.Logger
}
