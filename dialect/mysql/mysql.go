// Package mysql is liteorm's MySQL backend over go-sql-driver/mysql (database/sql).
// It wires the MySQL dialect (ON DUPLICATE KEY upsert, backtick quoting, LastInsertId)
// and normalizes MySQLError numbers to liteorm sentinels.
//
// License note: go-sql-driver/mysql is MPL-2.0 (file-level copyleft). It is
// imported unmodified here; do not edit the driver's files.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	driver "github.com/go-sql-driver/mysql"

	liteorm "liteorm.org"
	"liteorm.org/internal/sqladapter"
	"liteorm.org/internal/sqlgen"
)

// Open opens a MySQL database for the given DSN and returns a liteorm.DB on the
// MySQL dialect.
func Open(ctx context.Context, dsn string, opts ...liteorm.Option) (*liteorm.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return sqladapter.Open(db, sqlgen.MySQL, normalizeError, opts...), nil
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var my *driver.MySQLError
	if errors.As(err, &my) {
		switch my.Number {
		case 1062:
			return fmt.Errorf("%w: %w", liteorm.ErrUniqueViolation, err)
		case 1452:
			return fmt.Errorf("%w: %w", liteorm.ErrForeignKey, err)
		case 1048:
			return fmt.Errorf("%w: %w", liteorm.ErrNotNull, err)
		case 3819, 4025: // MySQL 8 / MariaDB CHECK
			return fmt.Errorf("%w: %w", liteorm.ErrCheck, err)
		case 1213, 1205: // deadlock / lock wait timeout (SQLSTATE 40001)
			// MySQL reports deadlocks and serialization-failure rollbacks alike
			// under 1213; check ErrDeadlock OR ErrSerialization when retrying.
			return fmt.Errorf("%w: %w", liteorm.ErrDeadlock, err)
		}
	}
	return err
}
