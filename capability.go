package liteorm

import (
	"context"
	"time"
)

// Capability interfaces are demand-driven: a backend implements one only when it
// can, and the front-ends type-assert for it and fall back otherwise. SQLite and
// MySQL implement LastInsertIder; the Postgres backend implements BulkInserter
// (native COPY) and adds LISTEN/NOTIFY. RETURNING/OUTPUT is reached directly via
// QueryContext, gated by the dialect's Feature bits, so it needs no interface.

// LastInsertIder is implemented by backends whose driver returns a usable
// last-insert id (SQLite, MySQL). Postgres/MSSQL use RETURNING/OUTPUT instead.
type LastInsertIder interface {
	LastInsertId() (int64, error)
}

// RowSource is liteorm's driver-neutral mirror of a bulk row stream (pgx's
// CopyFromSource shape, owned by liteorm so the driver type never leaks).
type RowSource interface {
	Next() bool
	Values() ([]any, error)
	Err() error
}

// BulkInserter is the bulk-insert fast path: the Postgres backend implements it
// with native COPY. Where a backend does not, the query layer falls back to
// chunked multi-row VALUES.
type BulkInserter interface {
	CopyFrom(ctx context.Context, table string, columns []string, src RowSource) (int64, error)
}

// PoolStats is liteorm's driver-neutral mirror of database/sql.DBStats, so the
// public surface reports connection-pool health without binding callers to
// database/sql (a backend may be pgx-based). Fields match DBStats one-for-one.
type PoolStats struct {
	MaxOpenConnections int           // configured max; 0 = unlimited
	OpenConnections    int           // in use + idle
	InUse              int           // currently in use
	Idle               int           // idle in the pool
	WaitCount          int64         // total connections waited for
	WaitDuration       time.Duration // total time blocked waiting for a connection
	MaxIdleClosed      int64         // closed due to SetMaxIdleConns
	MaxIdleTimeClosed  int64         // closed due to SetConnMaxIdleTime
	MaxLifetimeClosed  int64         // closed due to SetConnMaxLifetime
}

// StatsProvider is implemented by backends whose driver exposes connection-pool
// statistics — every database/sql-based backend (SQLite, MySQL, MSSQL) and the
// pgx-pool Postgres backend. DB.Stats() type-asserts for it and reports whether
// stats are available.
type StatsProvider interface {
	Stats() PoolStats
}
