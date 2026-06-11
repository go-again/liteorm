---
name: postgres-advanced
description: Use when working with Postgres-specific liteorm features — LISTEN/NOTIFY, or typed JSONB and array column operators.
---

# Postgres advanced

Import `liteorm.org/dialect/postgres`. These features require a session opened by `postgres.Open(ctx, dsn)`. The JSONB/array operators are gated by dialect features, so building them against another backend fails loudly at build time.

## LISTEN / NOTIFY

`Notify` sends on any pooled connection; `Listen` acquires one dedicated connection (LISTEN is connection-scoped) and `Receive` blocks for messages.

```go
// Publish
_ = postgres.Notify(ctx, db, "jobs", "payload-string")   // channel + payload bound, not interpolated

// Subscribe
l, err := postgres.Listen(ctx, db, "jobs")               // *Listener; sess must be a *liteorm.DB
defer l.Close(ctx)                                        // UNLISTEN + return conn to pool
for {
    n, err := l.Receive(ctx)                              // blocks; err == ctx.Err() on cancel
    if err != nil { return err }
    use(n.Channel, n.Payload, n.PID)                      // postgres.Notification{Channel, Payload string; PID uint32}
}
```

`l.Add(ctx, "more")` / `l.Remove(ctx, "ch")` adjust subscriptions on the same connection. A `Listener` is not safe for concurrent use — run one `Receive` loop per listener.

## JSONB operators

`query.JSON("col")` drills into object keys, then compares the extracted text or tests containment. Keys and values are always bound parameters.

```go
import "liteorm.org/query"

query.JSON("data").Key("city").Eq("Paris")                      // ->> path, text compare
query.JSON("data").Key("address").Key("city").Eq("Paris")       // nested: chain Key
query.JSON("data").Key("role").In("admin", "owner")             // Eq Ne Like In
query.JSON("data").Contains(map[string]any{"active": true})     // jsonb @> (value JSON-encoded)
```

Path extraction (`Eq`/`Ne`/`Like`/`In`) works on SQLite too; `Contains` (`@>`) is Postgres-only.

## Array operators

`query.Array[E]("col")` for a Postgres array column — all operators are Postgres-only.

```go
query.Array[string]("tags").Contains("go", "db")    // col @> ARRAY[...]   (has all)
query.Array[string]("tags").ContainedBy("go", "db") // col <@ ARRAY[...]
query.Array[string]("tags").Overlaps("go", "db")    // col && ARRAY[...]   (shares any)
query.Array[string]("tags").Has("go")               // 'go' = ANY(col)     (membership)
```

Use them in a normal `Filter`:

```go
query.Select[Doc](db).Filter(query.Array[string]("tags").Has("go")).All(ctx)
```

## Pitfalls

- `Listen` needs a `*liteorm.DB` opened by `dialect/postgres` — it returns an error otherwise.
- Always `Close` a `Listener` to UNLISTEN and release the dedicated connection.
- JSONB `Contains` and all array operators are Postgres-only; the query builder rejects them at BUILD time on other dialects (a clear error, not bad SQL). Plain JSON path extraction is the exception and works on SQLite.

## Deeper

- API: https://pkg.go.dev/liteorm.org/dialect/postgres and https://pkg.go.dev/liteorm.org/query
