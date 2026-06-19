module liteorm.org/conformance

go 1.25.7

require (
	liteorm.org v0.11.0
	liteorm.org/dialect/mssql v0.8.0
	liteorm.org/dialect/mysql v0.8.0
	liteorm.org/dialect/postgres v0.8.0
	liteorm.org/dialect/sqlite v0.8.0
	liteorm.org/gen v0.8.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.6 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/microsoft/go-mssqldb v1.10.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	gosqlite.org v0.9.0 // indirect
	gosqlite.org/vfs/crypto v0.9.0 // indirect
	lukechampine.com/adiantum v1.1.1 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.52.0 // indirect
)

replace liteorm.org => ..

replace liteorm.org/dialect/mssql => ../dialect/mssql

replace liteorm.org/dialect/mysql => ../dialect/mysql

replace liteorm.org/dialect/postgres => ../dialect/postgres

replace liteorm.org/dialect/sqlite => ../dialect/sqlite

replace liteorm.org/gen => ../gen
