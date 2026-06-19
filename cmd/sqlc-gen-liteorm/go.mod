module liteorm.org/cmd/sqlc-gen-liteorm

go 1.25.7

require (
	google.golang.org/protobuf v1.36.11
	liteorm.org/gen v0.9.0
)

require liteorm.org v0.9.0 // indirect

replace liteorm.org/gen => ../../gen

replace liteorm.org => ../..
