# Migrations

LiteORM has a deliberately two-track schema story. Additive changes — a new table, a new column, the indexes a model needs — are safe to apply automatically and do so via `AutoMigrate`. Destructive or ambiguous changes — dropping a column, retyping one, renaming — are never applied silently; they are emitted as reviewable SQL that you read, edit, and run through a migration runner on your own schedule. The two halves connect: a diff against your live database produces an up/down SQL pair that drops straight into the runner.

This guide covers both halves: the `orm` schema sync and diff helpers, and the standalone `liteorm.org/migrate` runner.

## Additive sync with AutoMigrate

`orm.AutoMigrate[T]` brings the table for model `T` into being and keeps it in sync, additively only. A table that does not exist is created (with its unique indexes and any many-to-many junction tables); an existing table gains a column for any model field the database is missing. It never drops a column, never alters a type — those are reviewable migrations, covered below.

```go
import "liteorm.org/orm"

type User struct {
	ID    int64
	Email string `orm:"email,unique,notnull"`
	Name  string
}

func (User) TableName() string { return "users" }

if err := orm.AutoMigrate[User](ctx, sess); err != nil {
	return err
}

// Or migrate a whole set in one call, in dependency order:
if err := orm.AutoMigrateAll(ctx, sess, User{}, Post{}, Comment{}); err != nil {
	return err
}
```

Because `AutoMigrate` iterates the *model* and never the live database, "never drop" is structural: there is no code path that removes a column you stopped mentioning. Unique indexes are created soft-delete-aware — a partial unique index (`... WHERE deleted_at IS NULL`) on SQLite, Postgres, and MSSQL, or a functional unique index on MySQL — so a soft-deleted row stops occupying the unique key.

Index sync is also additive: an `index` or `unique` tag you add to a model that already has a table is realized on the next `AutoMigrate` (the live table's indexes are listed and any the model declares but the database lacks are created). Removing an index is *not* automatic — a dropped tag surfaces as a reviewable migration, never a silent `DROP INDEX`.

`AutoMigrate` is a good fit for development and for additive production rollouts. When a change is destructive, reach for the diff helpers.

### Foreign-key constraints (opt-in)

By default LiteORM ships belongs-to and has-many relations as plain columns — no `FOREIGN KEY` constraints — so additive migration and bulk loads stay simple. When you do want them enforced, opt in:

```go
// every belongs-to relation on T gets a FOREIGN KEY on the newly created table
err := orm.AutoMigrate[Order](ctx, sess, orm.WithForeignKeys())
```

Or opt in a single relation with a tag, leaving the rest as plain columns:

```go
type Order struct {
	ID         int64
	CustomerID int64     `orm:"customer_id"`
	Customer   *Customer `orm:"fk:customer_id,constraint:fk"` // this FK is emitted
}
```

Constraints are emitted only into a *newly created* table, so migrate the referenced table first. Adding a constraint to a table that already exists is never automatic — it can fail on existing rows — so do that through a reviewable migration.

## Inspecting and diffing the live schema

`orm.IntrospectColumns` lists the existing columns of a table through the dialect's introspection capability. It returns an empty slice for a table that does not exist.

```go
cols, err := orm.IntrospectColumns(ctx, sess, "users")
for _, c := range cols {
	fmt.Println(c.Name, c.Type) // ColumnMeta{Name, Type}
}
```

`orm.Diff[T]` compares model `T` against the live table and reports the difference as `orm.Changes`:

```go
ch, err := orm.Diff[User](ctx, sess)
if ch.Empty() {
	// model and table already agree
}
// ch.Added   — fields in the model, missing in the DB (additive)
// ch.Removed — columns in the DB, missing from the model (destructive to drop)
// ch.Changed — columns whose type the model changed (reviewable: From/To)
```

`Added` is what `AutoMigrate` would apply; `Removed` and `Changed` are what it will never touch. Type-change detection canonicalizes both the model's type and the live catalog type before comparing, so cross-dialect spelling differences (`BIGSERIAL` vs `bigint`, `VARCHAR(255)` vs `varchar`, `TINYINT(1)` vs `tinyint`) don't register as spurious changes. It is deliberately conservative: when a type can't be canonicalized confidently it reports *no* change, because a missed change is a harmless reviewable no-op while a false one churns migrations.

## Generating a reviewable migration

`orm.GenerateMigration[T]` computes the diff and returns up/down SQL as strings. It does **not** execute anything — that is the whole point. Added columns become `ADD COLUMN` / `DROP COLUMN`; removed columns become a *commented* destructive `DROP`, and a type change becomes a *commented* dialect-specific `ALTER ... TYPE` (a manual-rebuild note on SQLite, which can't retype a column in place). So the up script is safe to run as-is and you opt in to the destructive parts by uncommenting after review.

```go
up, down, err := orm.GenerateMigration[User](ctx, sess)
if err != nil {
	return err
}
fmt.Println(up)   // "ALTER TABLE \"users\" ADD COLUMN ...;" plus commented drops
fmt.Println(down) // the reverse
```

To feed this output to the runner, write it as a migration pair (see [the bridge](#bridging-generated-sql-to-the-runner) below).

## The migration runner

`liteorm.org/migrate` is a thin, driver-free runner. It applies ordered SQL migrations against any `liteorm.Session`, tracking state in a single-row `(version, dirty)` ledger table created dialect-aware so it works across every backend. It does not generate DDL — it runs the SQL you (or `GenerateMigration`) wrote.

A migration is a versioned step. `Down` may be empty, marking the step irreversible.

```go
import "liteorm.org/migrate"

migs := []migrate.Migration{
	{Version: 1, Name: "create_users", Up: "CREATE TABLE users (...);", Down: "DROP TABLE users;"},
	{Version: 2, Name: "add_email", Up: "ALTER TABLE users ADD COLUMN email TEXT;", Down: "ALTER TABLE users DROP COLUMN email;"},
}
```

### Loading migrations from disk

`migrate.Load` reads migrations from the root of any `fs.FS` (for example an `embed.FS` subtree) and auto-detects three on-disk formats, so adopters keep their existing history:

- golang-migrate split files — `NNN_name.up.sql` and `NNN_name.down.sql`
- goose- or sql-migrate-annotated single files — `-- +goose Up` / `-- +goose Down`
- plain numbered single files — `NNN_name.sql` (up-only)

```go
import "embed"

//go:embed migrations/*.sql
var migrationsFS embed.FS

sub, _ := fs.Sub(migrationsFS, "migrations")
migs, err := migrate.Load(sub)
```

### Applying and rolling back

Construct a `Migrator` bound to a session, then drive it:

```go
m := migrate.New(sess) // or migrate.New(sess, migrate.WithTable("my_ledger"))

n, err := m.Up(ctx, migs)            // apply every pending migration; n = how many ran
n, err = m.UpTo(ctx, migs, 5)        // apply up to and including version 5
err = m.Down(ctx, migs)              // roll back the most recent step (one)
err = m.DownTo(ctx, migs, 2)         // roll back everything above version 2

ver, dirty, err := m.Version(ctx)    // current version and dirty flag
statuses, err := m.Status(ctx, migs) // []Status{Version, Name, Applied}
```

`Up` and `UpTo` skip already-applied versions and run pending ones in version order. `Down` requires a non-empty `Down` script — an irreversible step is reported as such rather than silently skipped.

### The dirty flag and recovery

If a migration fails part-way, the ledger is left **dirty** at that version and the next `Up`/`Down` refuses with a `*migrate.DirtyError`. Resolve the database by hand, then declare the true state:

```go
var de *migrate.DirtyError
if errors.As(err, &de) {
	// inspect the database, fix it manually, then:
	err = m.Force(ctx, de.Version) // sets the version and clears the dirty flag
}
```

## Bridging generated SQL to the runner

`migrate.WritePair` writes a golang-migrate-style pair into a directory:

```
<version>_<slug>.up.sql
<version>_<slug>.down.sql
```

The version is zero-padded, the slug is derived from the name, and the files are exactly the split format `Load` reads back. So a migration generated from a model diff drops straight into the runner an adopter already uses:

```go
up, down, _ := orm.GenerateMigration[User](ctx, sess)
upPath, downPath, err := migrate.WritePair("migrations", 3, "add user columns", up, down)
// later: migrate.Load(...) picks up the new pair
```

An empty `up` is an error; an empty `down` is written as an explicit comment so the step is recorded as irreversible rather than silently empty.

## See also

- [Errors](errors.md) — normalized constraint errors you will hit while migrating.
- [Backends reference](../reference/backends.md) — how to open each backend.
- [Dialects reference](../reference/dialects.md) — per-dialect DDL and quoting differences.
- Full API: [`liteorm.org/migrate`](https://pkg.go.dev/liteorm.org/migrate) and [`liteorm.org/orm`](https://pkg.go.dev/liteorm.org/orm).
