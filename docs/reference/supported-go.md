# Supported Go versions

LiteORM targets the two most recent Go releases. The exact lower bound is pinned in the module's `go.mod`; check there for the authoritative minimum. Supporting a narrow, current window keeps the codebase free to use modern language and standard-library features without compatibility shims.

## Modern idioms

LiteORM uses recent Go idioms freely, which is part of why the API feels current:

- **Generics** — the query and orm front-ends are generic over your model type (`query.Select[T]`, `orm.NewRepo[T]`), and typed columns (`query.Column[V]`) carry the column's value type through the compiler.
- **`iter.Seq2`** — range-over-function iterators are used for streaming result sets where they fit.
- **`log/slog`** — structured logging integrates with the standard `log/slog` package rather than a bespoke logger.

## Upgrading Go

Because the support window tracks the two most recent releases, upgrading your toolchain along with each Go release keeps you inside the supported range. If you must stay on an older Go, pin a LiteORM version that still supported it, but the current line assumes a current toolchain.

## See also

- [Backends reference](backends.md) — the backends and their drivers.
- Full API: [`liteorm.org`](https://pkg.go.dev/liteorm.org).
