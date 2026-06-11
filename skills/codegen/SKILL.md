---
name: codegen
description: Use when generating liteorm code — typed columns from a Go type, models from a live DB, typed Go from annotated SQL, the sqlc plugin, or porting gorm tags.
---

# codegen

`liteorm.org/gen` emits Go from three inputs. There is also a sqlc process plugin and a gorm tag porter. All paths are driver-free: `FromDB` takes a `liteorm.Session`, the caller wires the backend.

## Typed columns (compile-time column safety)

`FromType[T]` reflects over an existing model; `WriteColumns` emits a typed `TColumns` struct so predicates get compile-time COLUMN safety on top of the runtime-validated `query.Col`.

```go
import "liteorm.org/gen"

err := gen.WriteColumns(out /* io.Writer */, "models", gen.FromType[User]())
```

Generated usage:

```go
query.Select[User](db).Filter(UserColumns.Email.Eq("ada@x.io")).All(ctx)
```

## Models from a live DB

When a table has no Go type yet, introspect it and emit struct + TableName + columns:

```go
models, _ := gen.FromDB(ctx, sess, "users", "orders")
_ = gen.WriteModels(out, "models", models...)
```

`FromType` + `WriteColumns` is "struct is the source of truth, emit columns"; `FromDB` + `WriteModels` is "DB is the source of truth, emit the struct too."

## SQL → typed Go (annotated queries)

Parse sqlc-style `-- name: X :cmd` files into typed functions over the liteorm runtime. Result/arg Go types come from companion directives (this path does not parse SQL):

```sql
-- name: GetUser :one
-- liteorm:result User
-- liteorm:arg id int64
SELECT id, name FROM users WHERE id = ?;
```

```go
qs, _ := gen.ParseQueries(src)         // src is the .sql file contents
_ = gen.WriteQueries(out, "main", qs)  // emits GetUser(ctx, sess, id) (User, error)
```

Commands: `:one` (T, error; ErrNoRows when empty), `:many` ([]T, error), `:exec` (error), `:execrows` (int64 affected), `:execresult` (liteorm.Result), `:execlastid` (int64). `:one`/`:many` need a `-- liteorm:result <Type>` directive (unless the result is a scalar like `int64`/`string`, which is scanned directly). Args default to `any` and `argN` when undeclared.

Wire it with `go generate`:

```go
//go:generate go run ./generate
```

## sqlc-gen-liteorm (sqlc process plugin)

For teams already on sqlc: sqlc parses schema + queries and invokes `sqlc-gen-liteorm` as a process plugin, which emits the same liteorm-runtime typed functions. Configure in `sqlc.yaml`:

```yaml
version: "2"
plugins:
  - name: liteorm
    process:
      cmd: sqlc-gen-liteorm
sql:
  - schema: schema.sql
    queries: query.sql
    engine: postgresql
    codegen:
      - plugin: liteorm
        out: db
        options: { package: db }
```

This path does parse SQL (sqlc does it), so result/arg types are inferred — no `liteorm:` directives needed.

## Gorm tag porter (PortSource)

Rewrite `gorm:"..."` struct tags into native `orm:"..."` tags (and `gorm.DeletedAt` → `sql.NullTime` + `soft_delete`) on Go source, so a gorm codebase can drop the gorm dependency. It edits only tag literals and that one field type; everything else stays byte-for-byte.

```go
out, notes, err := gen.PortSource(src /* []byte of Go source */)
// out = rewritten source; notes = []gen.Note{Pos, Message} (dropped keys, import hints)
```

After porting, run `goimports -w` (gorm.io/gorm is now unused; database/sql is required). The orm package reads gorm tags natively, so porting is about cleanliness, not making it work.

## Pitfalls

- `WriteColumns`/`WriteModels`/`WriteQueries` gofmt their output and error if it won't parse — fix the input types, not the generated file (it's `DO NOT EDIT`).
- In the annotated-SQL path, a `:one`/`:many` non-scalar result without a `-- liteorm:result` directive is an error.
- Emitted result/arg type strings are verbatim — they must be valid in the generated package (a local type, a builtin, or a wired import).

## Deeper

- Guide: [../../docs/guides/query.md](../../docs/guides/query.md)
- API: https://pkg.go.dev/liteorm.org/gen
