# Code generation

LiteORM's runtime is fully usable without generating anything: `query.Col[V]("name")` builds typed predicate tokens, and the orm front-end reflects over your structs. Code generation buys you *compile-time* safety on top of that runtime — typed column constants the compiler checks, model structs lifted from a live database, and typed Go functions for hand-written SQL. The generator is in `liteorm.org/gen`; a sqlc plugin and a gorm-tag porter round out the toolkit.

Pick the mode that fits where your source of truth lives:

- Your Go structs are canonical, and you want compile-time-safe columns → [typed columns](#typed-columns-from-a-go-type).
- The database is canonical, and you have no Go types yet → [models from a live database](#models-from-a-live-database).
- You write SQL by hand and want typed Go wrappers → [SQL to typed Go](#sql-to-typed-go) or [the sqlc plugin](#the-sqlc-plugin).
- You are porting a gorm codebase → [the gorm porter](#the-gorm-porter).

The `gen` package is driver-free: the DB-introspecting paths take a `liteorm.Session`, so you wire the backend.

## Typed columns from a Go type

`gen.FromType[T]` reflects over an existing model; `gen.WriteColumns` emits a typed column struct for it. The struct stays the source of truth — the generator only mirrors its columns.

```go
import "liteorm.org/gen"

f, _ := os.Create("usercols.gen.go")
defer f.Close()
gen.WriteColumns(f, "models", gen.FromType[User]())
```

The output is a struct of `query.Column[V]` values:

```go
var UserColumns = struct {
	ID    query.Column[int64]
	Name  query.Column[string]
	Email query.Column[string]
}{
	ID:    query.Col[int64]("id"),
	Name:  query.Col[string]("name"),
	Email: query.Col[string]("email"),
}
```

You then build queries against typed columns — a typo or a wrong-typed comparison is a compile error rather than a runtime surprise:

```go
users, err := query.Select[User](db).Filter(UserColumns.Name.Eq("alice")).All(ctx)
```

## Models from a live database

When the database already exists and you have no Go types for some tables, `gen.FromDB` introspects them and `gen.WriteModels` emits the struct *plus* its `TableName` method *plus* the typed columns. Each SQL column type is mapped to a Go type per dialect.

```go
models, err := gen.FromDB(ctx, sess, "users", "orders")
if err != nil {
	return err
}
gen.WriteModels(f, "models", models...)
```

Use `WriteModels` for DB-introspected models (no Go type yet) and `WriteColumns` for models whose struct you already maintain.

## SQL to typed Go

The `gen` package also turns annotated `.sql` files into typed Go functions over the LiteORM runtime. It uses the sqlc annotation grammar — `-- name: Name :cmd` — so existing sqlc query files parse unchanged. The command verb selects the function shape:

| Command | Returns |
| --- | --- |
| `:one` | `(T, error)` — `ErrNoRows` when empty |
| `:many` | `([]T, error)` |
| `:exec` | `error` |
| `:execrows` | `(int64, error)` — rows affected |
| `:execresult` | `(liteorm.Result, error)` |
| `:execlastid` | `(int64, error)` — last insert id |

This mode does not parse the SQL itself, so the result type and argument types are supplied by lightweight LiteORM directives — `-- liteorm:result <Type>` and `-- liteorm:arg <name> <type>`:

```sql
-- name: GetUser :one
-- liteorm:result User
-- liteorm:arg id int64
SELECT id, name, email FROM users WHERE id = ?;

-- name: ListActive :many
-- liteorm:result User
SELECT id, name, email FROM users WHERE active = ?;
```

Result and arg types are emitted verbatim, so they must be valid in the generated package — a local type like `User`, a builtin like `int64`, or an imported type you wire up. The `:exec` family needs no result directive; argument count is inferred from the `?` / `$N` placeholders when you omit `-- liteorm:arg`.

Generate from a Go program:

```go
src, _ := os.ReadFile("queries.sql")
queries, err := gen.ParseQueries(string(src))
if err != nil {
	return err
}
gen.WriteQueries(f, "db", queries)
```

A `:one` returning a struct is backed by `query.Raw`; a scalar `:one` (`int64`, `string`, …) is read with a plain `Scan`. See `examples/queries` for the full `go generate` loop.

## The sqlc plugin

If you already run sqlc, the standalone process plugin `liteorm.org/cmd/sqlc-gen-liteorm` emits the same LiteORM-runtime functions with **full type inference and no annotations**. sqlc parses your schema and queries, infers each query's result columns and parameter types from the catalog, and hands the typed result to the plugin — so multi-column reads get a generated row struct and every argument is typed for you.

Build the plugin binary, then wire it in `sqlc.yaml`:

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

This complements the annotation-based generator: the plugin is zero-annotation but requires a sqlc-parseable schema; `gen.ParseQueries` needs the type directives but parses any SQL.

## The gorm porter

`gen.PortSource` is a source-to-source rewriter, not a runtime emulator. It parses Go source, rewrites every `gorm:"..."` struct tag into LiteORM's native `orm:"..."` tag, and rewrites the `gorm.DeletedAt` field type to `sql.NullTime` (tagged `soft_delete`) — so a gorm codebase can drop the `gorm.io/gorm` dependency and keep idiomatic, LiteORM-native models. Only tag literals and the `DeletedAt` type are touched; all other formatting is preserved.

```go
out, notes, err := gen.PortSource(src)
if err != nil {
	return err
}
os.Stdout.Write(out)
for _, n := range notes {
	fmt.Printf("%s  %s\n", n.Pos, n.Message) // line:col, message
}
```

The returned notes flag things you should handle by hand — an embedded `gorm.Model` (reported with the suggested explicit fields), dropped unsupported tag keys, and a reminder to run `goimports` since `gorm.io/gorm` is likely now unused and `database/sql` is now required. LiteORM's `orm` package reads gorm tags directly anyway, so a ported model behaves identically to the original; the port is about cleanliness and dropping the dependency. See `examples/gormport` for a model ported and run live.

## See also

- Examples: `examples/codegen`, `examples/queries`, `examples/gormport`.
- [Migrations](migrations.md) — `gen.FromDB` and `orm.IntrospectColumns` share the same introspection.
- Full API: [`liteorm.org/gen`](https://pkg.go.dev/liteorm.org/gen) and [`liteorm.org/cmd/sqlc-gen-liteorm`](https://pkg.go.dev/liteorm.org/cmd/sqlc-gen-liteorm).
