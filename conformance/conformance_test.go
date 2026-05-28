package conformance_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	liteorm "liteorm.org"
	"liteorm.org/conformance"
	"liteorm.org/dialect/mssql"
	"liteorm.org/dialect/mysql"
	"liteorm.org/dialect/postgres"
	"liteorm.org/dialect/sqlite"
)

const sqliteSchema = `CREATE TABLE users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	email      TEXT NOT NULL UNIQUE,
	created_at TIMESTAMP NOT NULL
)`

const pgSchema = `CREATE TABLE users (
	id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	name       TEXT NOT NULL,
	email      TEXT NOT NULL UNIQUE,
	created_at TIMESTAMPTZ NOT NULL
)`

const mysqlSchema = `CREATE TABLE users (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	name       VARCHAR(255) NOT NULL,
	email      VARCHAR(255) NOT NULL UNIQUE,
	created_at DATETIME NOT NULL
)`

const mssqlSchema = `CREATE TABLE users (
	id         BIGINT IDENTITY(1,1) PRIMARY KEY,
	name       NVARCHAR(255) NOT NULL,
	email      NVARCHAR(255) NOT NULL UNIQUE,
	created_at DATETIME2 NOT NULL
)`

func TestSQLite(t *testing.T) {
	conformance.Run(t, conformance.Backend{
		Name:   "sqlite",
		Schema: sqliteSchema,
		Open: func() (*liteorm.DB, error) {
			dir, err := os.MkdirTemp("", "liteorm-conf-*")
			if err != nil {
				return nil, err
			}
			return sqlite.Open(filepath.Join(dir, "conf.db"))
		},
	})
}

// TestPostgres runs the same suite against Postgres when LITEORM_PG_DSN is set
// (e.g. in CI). It is skipped otherwise so local/offline runs stay green.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("LITEORM_PG_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_PG_DSN to run the Postgres conformance suite")
	}
	conformance.Run(t, conformance.Backend{
		Name:   "postgres",
		Schema: pgSchema,
		Reset:  "DROP TABLE IF EXISTS users",
		Open: func() (*liteorm.DB, error) {
			return postgres.Open(context.Background(), dsn)
		},
	})
}

// TestMySQL runs the suite against MySQL when LITEORM_MYSQL_DSN is set.
func TestMySQL(t *testing.T) {
	dsn := os.Getenv("LITEORM_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_MYSQL_DSN to run the MySQL conformance suite")
	}
	conformance.Run(t, conformance.Backend{
		Name:   "mysql",
		Schema: mysqlSchema,
		Reset:  "DROP TABLE IF EXISTS users",
		Open: func() (*liteorm.DB, error) {
			return mysql.Open(context.Background(), dsn)
		},
	})
}

// TestMSSQL runs the suite against SQL Server when LITEORM_MSSQL_DSN is set.
func TestMSSQL(t *testing.T) {
	dsn := os.Getenv("LITEORM_MSSQL_DSN")
	if dsn == "" {
		t.Skip("set LITEORM_MSSQL_DSN to run the MSSQL conformance suite")
	}
	conformance.Run(t, conformance.Backend{
		Name:   "mssql",
		Schema: mssqlSchema,
		Reset:  "DROP TABLE IF EXISTS users",
		Open: func() (*liteorm.DB, error) {
			return mssql.Open(context.Background(), dsn)
		},
	})
}
