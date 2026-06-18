# Hooks

Hooks are typed, context-first lifecycle methods that run around writes through the [orm repository](orm.md). You opt a model into a hook by implementing a method on `*T`; the repository fires it automatically on `Create`, `Update`, or `Delete`. Hooks are compile-checked against the model type, so a wrong signature is a build error — never a silently-dead callback that drops your logic on the floor.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/orm](https://pkg.go.dev/liteorm.org/orm).

## The lifecycle methods

There are six hook points, one before and one after each write operation:

| Operation | Before | After |
| --- | --- | --- |
| `Create` | `BeforeCreate` | `AfterCreate` |
| `Update` | `BeforeUpdate` | `AfterUpdate` |
| `Delete` | `BeforeDelete` | `AfterDelete` |

Every hook has the same signature, taking a `context.Context` and a typed `*orm.Event[T]`, and returning an `error`:

```go
func (p *Post) BeforeCreate(ctx context.Context, ev *orm.Event[Post]) error {
	if ev.Model.Slug == "" {
		ev.Model.Slug = slugify(ev.Model.Title)
	}
	return nil
}
```

Implement only the hooks you need. The repository checks once per type which hook interfaces `*T` satisfies, so models without hooks pay nothing.

## The Op handle

`orm.Event[T]` is the narrow, explicit handle passed to every hook. It carries exactly two things:

- `ev.Model` — the typed `*T` being written. Mutate it in a `Before*` hook to change what gets persisted (the slug example above), or read it in an `After*` hook.
- `ev.Sess` — the executing `liteorm.Session`. When the write runs inside a transaction, this is that transaction, so anything you do through it commits or rolls back atomically with the write — see [transactions](transactions.md).

```go
func (p *Post) AfterCreate(ctx context.Context, ev *orm.Event[Post]) error {
	// ev.Sess is the same session (or tx) the Create ran on.
	return orm.NewRepo[AuditEntry](ev.Sess).
		Create(ctx, &AuditEntry{Action: "post.created", PostID: ev.Model.ID})
}
```

In an `AfterCreate` hook the model's generated primary key is already populated, because the repository reads it back before firing the hook.

## Errors abort the operation

A hook that returns a non-nil error stops the write. A `Before*` error aborts before any SQL runs; an `After*` error surfaces from the repository call after the row was written — inside a transaction you'd roll back. Errors are never swallowed, so a failed validation in `BeforeCreate` reliably prevents the insert:

```go
func (u *User) BeforeCreate(ctx context.Context, ev *orm.Event[User]) error {
	if ev.Model.Email == "" {
		return fmt.Errorf("user: email is required")
	}
	return nil
}
```

```go
if err := users.Create(ctx, &User{}); err != nil {
	// err is the hook's error; no row was inserted
}
```

## Compile-checked opt-in

Each hook point is a typed interface — `orm.BeforeCreateHook[T]`, `orm.AfterUpdateHook[T]`, and so on, all parameterized on your model type `T`. A satisfying method must match the exact signature, which means a typo in the method name or a wrong parameter type won't compile if you assert the interface. Pin it with a blank assignment so a mistake is caught at build time rather than discovered at runtime:

```go
var _ orm.BeforeCreateHook[Post] = (*Post)(nil)
```

This is the recommended pattern for every hook you implement.

## Where to next

- [The orm front-end](orm.md) — the repository whose writes fire these hooks.
- [Transactions](transactions.md) — `ev.Sess` is the tx your hook runs in.
- [Soft delete](soft-delete.md) — `BeforeDelete` / `AfterDelete` fire on soft deletes too.
