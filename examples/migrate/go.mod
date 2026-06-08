module liteorm.org/examples/migrate

go 1.25.7

require (
	liteorm.org v0.0.0
	liteorm.org/dialect/sqlite v0.0.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gosqlite.org v0.0.0 // indirect
	lukechampine.com/adiantum v1.1.1 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.52.0 // indirect
)

replace gosqlite.org => ../../.gosqlite

replace liteorm.org => ../..

replace liteorm.org/dialect/sqlite => ../../dialect/sqlite
