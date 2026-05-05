// Package mssql is liteorm's SQL Server backend over microsoft/go-mssqldb
// (database/sql). It wires the MSSQL dialect (@pN placeholders, [bracket] quoting,
// MERGE upsert, OUTPUT instead of RETURNING, OFFSET/FETCH pagination, SAVE
// TRANSACTION savepoints) and normalizes mssql.Error numbers to liteorm sentinels.
package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mssqldriver "github.com/microsoft/go-mssqldb"

	liteorm "liteorm.org"
	"liteorm.org/internal/sqladapter"
	"liteorm.org/internal/sqlgen"
)

// Open opens a SQL Server database for the given DSN (sqlserver://… or ADO form)
// and returns a liteorm.DB on the MSSQL dialect.
func Open(ctx context.Context, dsn string, opts ...liteorm.Option) (*liteorm.DB, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return sqladapter.Open(db, sqlgen.MSSQL, normalizeError, opts...), nil
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var me mssqldriver.Error
	if errors.As(err, &me) {
		switch me.Number {
		case 2627, 2601: // PK/unique constraint or unique index
			return fmt.Errorf("%w: %w", liteorm.ErrUniqueViolation, err)
		case 515: // cannot insert NULL
			return fmt.Errorf("%w: %w", liteorm.ErrNotNull, err)
		case 1205: // deadlock victim
			return fmt.Errorf("%w: %w", liteorm.ErrDeadlock, err)
		case 547: // FK and CHECK share 547 — disambiguate on the message
			if strings.Contains(me.Message, "CHECK") {
				return fmt.Errorf("%w: %w", liteorm.ErrCheck, err)
			}
			return fmt.Errorf("%w: %w", liteorm.ErrForeignKey, err)
		}
	}
	return err
}
