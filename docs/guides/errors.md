# Errors

Every backend reports constraint violations and transaction failures in its own dialect — a SQLite extended result code, a Postgres SQLSTATE, a MySQL error number. LiteORM normalizes these into a small set of typed sentinel errors you can test with `errors.Is`, so the same code recognizes a unique-constraint violation whether it ran against SQLite or Postgres.

The normalization is non-destructive: each backend dual-wraps the sentinel *and* the original driver error, so `errors.Is(err, liteorm.ErrUniqueViolation)` and `errors.As(err, &driverErr)` both keep working. You lose nothing by getting the portable sentinel.

## The sentinels

```go
import liteorm "liteorm.org"

var (
	liteorm.ErrNoRows           // single-row read found nothing
	liteorm.ErrUniqueViolation  // unique / primary-key constraint
	liteorm.ErrForeignKey       // foreign-key constraint
	liteorm.ErrNotNull          // not-null constraint
	liteorm.ErrCheck            // check constraint
	liteorm.ErrDeadlock         // deadlock detected
	liteorm.ErrSerialization    // serialization failure (retryable)
)
```

`liteorm.ErrNoRows` *is* `database/sql.ErrNoRows`, so `errors.Is` against either works uniformly across backends.

Not every backend can produce every sentinel — `ErrSerialization`, for instance, comes from databases with serializable isolation. Test for the ones relevant to your code; an unrecognized error passes through unchanged so you can still inspect it.

## Classifier helpers

For the common checks there are one-line predicates over the sentinels, so you can skip spelling out `errors.Is`: `liteorm.IsUniqueViolation(err)`, `IsForeignKeyViolation`, `IsNotNullViolation`, `IsCheckViolation`, `IsNotFound`, and `IsRetryable` (true for a deadlock or serialization failure). Each matches anywhere in the wrapped error chain and is exactly equivalent to the corresponding `errors.Is`.

## Detecting a unique-violation

```go
err := repo.Create(ctx, &User{Email: "ada@example.com"})
if liteorm.IsUniqueViolation(err) {
	// e.g. surface a 409 Conflict: the email is already taken
	return errEmailTaken
}
if err != nil {
	return err
}
```

This branch fires identically on a SQLite `SQLITE_CONSTRAINT_UNIQUE`, a Postgres `23505`, and a MySQL duplicate-key error.

## Detecting not-found

Single-row reads return `liteorm.ErrNoRows` when nothing matches:

```go
user, err := repo.Get(ctx, id)
if errors.Is(err, liteorm.ErrNoRows) {
	return errUserNotFound
}
if err != nil {
	return err
}
// use user
```

## Recovering the driver error

Because the original error is wrapped alongside the sentinel, you can still reach the driver-specific type when you need the raw code or message:

```go
import "github.com/jackc/pgx/v5/pgconn"

var pgErr *pgconn.PgError
if errors.As(err, &pgErr) {
	log.Printf("postgres %s: %s", pgErr.Code, pgErr.ConstraintName)
}
```

## Retrying transient failures

`ErrDeadlock` and `ErrSerialization` indicate the transaction can be retried:

```go
for attempt := 0; attempt < maxRetries; attempt++ {
	err := doWork(ctx)
	if liteorm.IsRetryable(err) { // deadlock or serialization failure
		continue // back off and retry
	}
	return err
}
```

## See also

- [Migrations](migrations.md) — `ErrUniqueViolation` and friends show up while syncing schema.
- [Backends reference](../reference/backends.md) — which sentinels each backend can produce.
- Full API: [`liteorm.org`](https://pkg.go.dev/liteorm.org).
