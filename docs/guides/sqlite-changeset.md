# SQLite changesets

The `liteorm.org/dialect/sqlite/changeset` package exposes SQLite's SESSION extension through LiteORM. A changeset is a compact binary diff of the rows a set of statements touched. You can capture one on a database, invert it, concatenate it with another, and apply it to a different database with a Go conflict handler. The classic uses are audit logs, one-way replication, and undo.

Like the search package, this is SQLite-only and capability-gated: `Capture` and `Apply` take a `liteorm.Session` opened by `liteorm.org/dialect/sqlite`.

## Capturing changes

`changeset.Capture` records every mutation a function makes to the listed tables and returns the serialized changeset. It pins a dedicated connection so the recording session and the mutations share one physical connection — so the mutations inside the function **must run against the session `Capture` hands back**, not the original handle.

```go
import "liteorm.org/dialect/sqlite/changeset"

cs, err := changeset.Capture(ctx, db, []string{"users", "orders"},
	func(ctx context.Context, s liteorm.Session) error {
		// Mutations MUST use s — the pinned session — to be recorded.
		_, err := s.ExecContext(ctx, `UPDATE users SET name = ? WHERE id = ?`, "Ada", 1)
		return err
	},
)
```

Pass an empty (or nil) table slice to record every table that has a primary key. The returned `cs` is a `[]byte` you can store, send over a wire, or apply elsewhere.

## Applying a changeset

`changeset.Apply` replays a captured changeset onto another database — the same one, or a replica:

```go
err := changeset.Apply(ctx, replica, cs)
```

With no conflict handler, any conflict aborts the whole apply. To resolve conflicts row by row, pass `changeset.WithConflictHandler`. It receives the conflict type and returns an action:

```go
err := changeset.Apply(ctx, replica, cs,
	changeset.WithConflictHandler(func(t changeset.ConflictType) changeset.ConflictAction {
		switch t {
		case changeset.ConflictNotFound:
			return changeset.Omit    // skip this change
		case changeset.ConflictData:
			return changeset.Replace // overwrite the target row
		default:
			return changeset.Abort   // abort the whole apply
		}
	}),
)
```

The conflict types are `ConflictData`, `ConflictNotFound`, `ConflictConflict`, `ConflictConstraint`, and `ConflictForeignKey`; the actions are `Omit`, `Replace`, and `Abort`. `changeset.WithTableFilter` restricts which tables a changeset applies to.

## Inverting, concatenating

`changeset.Invert` reverses a changeset — applying the result undoes the original, which makes it an undo log:

```go
undo, err := changeset.Invert(ctx, db, cs)
// applying undo reverses what cs did
err = changeset.Apply(ctx, db, undo)
```

`changeset.Concat` joins two changesets into one, as if the second were recorded immediately after the first:

```go
combined, err := changeset.Concat(ctx, db, csA, csB)
```

## Use cases

- **Audit log** — capture every mutation of a request, store the changeset; the binary diff is a precise record of what changed.
- **One-way replication** — capture on a primary, ship the changeset, apply on a replica with a conflict handler for divergence.
- **Undo** — keep the inverted changeset; apply it to roll a transaction's effect back later.

## See also

- [SQLite search](sqlite-search.md) — the other SQLite-only extension.
- [Backends reference](../reference/backends.md) — the SQLite backend.
- Full API: [`liteorm.org/dialect/sqlite/changeset`](https://pkg.go.dev/liteorm.org/dialect/sqlite/changeset).
