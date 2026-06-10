# Postgres features

LiteORM's Postgres backend runs over the native pgx/v5 API (pgxpool, not the `database/sql` shim), so you get the binary protocol scan path and pgx's native fast paths. On top of the portable query and orm front-ends, a handful of Postgres-specific capabilities are available: `RETURNING`, `CopyFrom` bulk insert, `LISTEN`/`NOTIFY`, and JSONB / array operators.

Open a Postgres database with `liteorm.org/dialect/postgres`:

```go
import "liteorm.org/dialect/postgres"

db, err := postgres.Open(ctx, "postgres://user:pass@host:5432/dbname")
```

## RETURNING and bulk insert

The native pgx backend reports rows affected via `RETURNING` and supports `CopyFrom`, Postgres's `COPY`-based bulk insert — the fast path for loading many rows. These are surfaced through the portable front-ends; see the query and orm guides for how inserts opt into `RETURNING`, and the [backends reference](../reference/backends.md) for the capability matrix.

## LISTEN / NOTIFY

`postgres.Listen` subscribes to one or more channels on a single dedicated connection (LISTEN is connection-scoped, so it cannot ride the pool's round-robin). `Receive` blocks until a notification arrives or the context is cancelled.

```go
l, err := postgres.Listen(ctx, db, "jobs")
if err != nil {
	return err
}
defer l.Close(ctx)

for {
	n, err := l.Receive(ctx) // postgres.Notification{Channel, Payload, PID}
	if err != nil {
		return err // ctx.Err() on cancellation
	}
	fmt.Printf("on %s from pid %d: %s\n", n.Channel, n.PID, n.Payload)
}
```

A `Listener` is not safe for concurrent use — run one `Receive` loop per listener. Subscribe to more channels with `l.Add(ctx, "other")`, drop one with `l.Remove(ctx, "jobs")`, and always `Close` it to release the connection back to the pool.

Send a notification from any pooled connection with `postgres.Notify`:

```go
err := postgres.Notify(ctx, db, "jobs", "job-42 ready")
```

The channel name is passed as a bound parameter to `pg_notify`, so there is no identifier interpolation to get wrong.

## JSONB and array operators

JSONB containment and the array operators live in the `liteorm.org/query` package and are gated to Postgres: building them against another dialect fails at build time rather than producing opaque SQL. Path keys and values are always bound parameters — never interpolated.

### JSON path extraction and containment

`query.JSON` names a JSON/JSONB column. Drill into object keys with `Key`, then compare the extracted text or test containment. Path extraction (`->` / `->>`) works on both Postgres and SQLite; the `@>` containment operator (`Contains`) is Postgres-only.

```go
import "liteorm.org/query"

// data->>'city' = 'Paris'
query.JSON("data").Key("address").Key("city").Eq("Paris")

// other comparisons on the extracted text
query.JSON("data").Key("status").In("open", "pending")
query.JSON("data").Key("name").Like("Ada%")

// jsonb containment (Postgres only): data @> '{"active":true}'
query.JSON("data").Contains(map[string]any{"active": true})
```

### Array operators

`query.Array[E]` names a Postgres array column of element type `E`. All its operators are Postgres-only.

```go
// tags @> ARRAY['go','sql'] — column contains all of these
query.Array[string]("tags").Contains("go", "sql")

// tags && ARRAY['go','rust'] — column shares at least one element
query.Array[string]("tags").Overlaps("go", "rust")

// 'go' = ANY(tags) — element membership
query.Array[string]("tags").Has("go")

// tags <@ ARRAY['go','sql','rust'] — every element is in this set
query.Array[string]("tags").ContainedBy("go", "sql", "rust")
```

Use these predicates anywhere a filter is accepted in the query front-end.

## See also

- [Backends reference](../reference/backends.md) — capability matrix and DSN shapes.
- [Dialects reference](../reference/dialects.md) — placeholder style, quoting, and feature flags.
- [Errors](errors.md) — Postgres SQLSTATEs normalized to portable sentinels.
- Full API: [`liteorm.org/dialect/postgres`](https://pkg.go.dev/liteorm.org/dialect/postgres) and [`liteorm.org/query`](https://pkg.go.dev/liteorm.org/query).
