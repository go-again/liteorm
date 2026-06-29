# Hooks

Hooks are typed, context-first lifecycle methods that run around operations through the [orm repository](orm.md). You opt a model into a hook by implementing a method on `*T`; the repository fires it automatically. Hooks are compile-checked against the model type, so a wrong signature is a build error — never a silently-dead callback that drops your logic on the floor.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/orm](https://pkg.go.dev/liteorm.org/orm).

## The lifecycle methods

| Operation | Before | After |
| --- | --- | --- |
| `Create` | `BeforeSave`, `BeforeCreate` | `AfterCreate`, `AfterSave` |
| `Update` | `BeforeSave`, `BeforeUpdate` | `AfterUpdate`, `AfterSave` |
| `Delete` | `BeforeDelete` | `AfterDelete` |
| read | — | `AfterFind` |

`BeforeSave`/`AfterSave` fire on **both** create and update — `BeforeSave` runs before the operation-specific `BeforeCreate`/`BeforeUpdate`, and `AfterSave` runs after `AfterCreate`/`AfterUpdate` — so a validation or normalization common to every write lives in one place. `AfterFind` runs after a read hydrates a row (covered [below](#afterfind-the-read-hook)).

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

## The Event handle

`orm.Event[T]` is the narrow, explicit handle passed to every hook. It carries:

- `ev.Model` — the typed `*T` being operated on. Mutate it in a `Before*` hook to change what gets persisted (the slug example above), or read it in an `After*` / `AfterFind` hook.
- `ev.Sess` — the executing `liteorm.Session`. When the write runs inside a transaction, this is that transaction, so anything you do through it commits or rolls back atomically with the write — see [transactions](transactions.md).
- `ev.Columns` — on `Create` and `Update`, the resolved set of columns the write touches (after any `Select`/`Omit`); nil on read and delete hooks. An audit hook can record exactly which columns changed without diffing the row by hand.

```go
func (p *Post) AfterCreate(ctx context.Context, ev *orm.Event[Post]) error {
	// ev.Sess is the same session (or tx) the Create ran on.
	return orm.NewRepo[AuditEntry](ev.Sess).
		Create(ctx, &AuditEntry{Action: "post.created", PostID: ev.Model.ID})
}
```

In an `AfterCreate` hook the model's generated primary key is already populated, because the repository reads it back before firing the hook.

## AfterFind: the read hook

`AfterFind` fires after a read hydrates a row, on every orm repository read path — `Get`, `GetByKeys`, `First`, `Find`, `FindInBatches` (once per element), and the found path of `FirstOrCreate`/`FirstOrInit`. Use it to derive a transient field, decrypt or transform a value, or enrich the model on the way out:

```go
func (u *User) AfterFind(ctx context.Context, ev *orm.Event[User]) error {
	u.DisplayName = strings.TrimSpace(u.First + " " + u.Last)
	return nil
}
```

It does not fire when a read matches nothing (`Get` of a missing key returns `ErrNoRows` and runs no hook). A returned error fails the read, symmetric with how `BeforeCreate` aborts a write.

`AfterFind` is an **orm repository** hook — it does not fire on a bare `query.Select[T]` read. The `query` builder is the explicit, magic-free front-end by design; reach for the repository when you want lifecycle hooks, and `query` when you want exactly the SQL you wrote and nothing more. (For a transform that must apply on *both* front-ends — like decrypting a column — use a [field codec](field-codecs.md) instead, which lives below both.)

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
