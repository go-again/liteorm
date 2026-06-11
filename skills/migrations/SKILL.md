---
name: migrations
description: Use when evolving a liteorm schema — AutoMigrate for additive changes, GenerateMigration/Diff for reviewable SQL, the migrate runner (Load/New/Up/Down), or WritePair.
---

# migrations

liteorm separates the two cases. Additive changes auto-apply; destructive ones become reviewable SQL you run through the migrate runner. The runner (`liteorm.org/migrate`) executes SQL but never generates DDL; the orm package generates DDL but only auto-applies additions.

## Two tracks

| Change | Tool |
| --- | --- |
| Add a table / column / index | `orm.AutoMigrate[T]` — applies immediately, never drops or alters types |
| Drop / retype / anything destructive | `orm.GenerateMigration[T]` — returns reviewable up/down SQL, executes nothing |

## AutoMigrate (additive)

```go
_ = orm.AutoMigrate[User](ctx, sess)
_ = orm.AutoMigrate[Order](ctx, sess, orm.WithForeignKeys()) // opt-in FK constraints
_ = orm.AutoMigrateAll(ctx, sess, User{}, Order{}, Item{})   // one-liner for a set, in order (no options)
```

Creates a missing table (plus its unique indexes and m2m junctions) or `ADD COLUMN`s for fields the model gained. Also syncs **indexes** additively: an `index`/`unique` tag added to an existing model's field is created on the next migrate (a removed tag is reviewable, never an auto `DROP INDEX`). Iterates the model, never the DB, so it can never drop.

**Foreign keys are opt-in** (off by default — relations ship as plain columns): `orm.WithForeignKeys()` emits a `FOREIGN KEY` for every belongs-to on a *newly created* table, or `orm:"constraint:fk"` opts in one relation. Migrate the referenced table first; adding a constraint to an existing table is reviewable-only.

## Diff / GenerateMigration (reviewable)

```go
ch, _ := orm.Diff[User](ctx, sess)   // Changes{Added []*Field, Removed []ColumnMeta, Changed []ColumnChange}; ch.Empty()

up, down, _ := orm.GenerateMigration[User](ctx, sess)
// up/down are SQL strings — NOT executed. Added cols become ADD/DROP;
// removed cols become a commented "-- destructive" DROP; a type change becomes a
// commented "-- reviewable type change" ALTER (a rebuild note on SQLite).
```

`ch.Changed` reports columns whose type the model changed. Detection canonicalizes both sides before comparing, so cross-dialect spellings (`BIGSERIAL`/`bigint`, `VARCHAR(255)`/`varchar`, `TINYINT(1)`/`tinyint`) don't false-positive; an un-canonicalizable type reports *no* change (conservative — a missed change is a safe no-op, a false one churns migrations). Type changes are never auto-applied. Index introspection is the optional `orm.IntrospectIndexes(ctx, sess, table)`.

You review the SQL, then either run it yourself or feed it into the runner via `WritePair`.

## The migrate runner (Load / New / Up / Down)

State lives in a single-row `(version, dirty)` ledger table (default `schema_migrations`), created dialect-aware. A failed step leaves the ledger dirty; the next run refuses until you `Force`.

```go
import (
    "embed"
    "liteorm.org/migrate"
)

//go:embed migrations/*.sql
var migFS embed.FS

migs, _ := migrate.Load(migFS)        // reads an fs.FS subtree
m := migrate.New(sess)                // also migrate.WithTable("name")
n, err := m.Up(ctx, migs)             // apply all pending, in version order
```

| Method | Does |
| --- | --- |
| `m.Up(ctx, migs)` | Apply every pending migration (returns count). |
| `m.UpTo(ctx, migs, target)` | Apply up to and including `target`. |
| `m.Down(ctx, migs)` | Roll back the most recent step (errors if its down is empty). |
| `m.DownTo(ctx, migs, target)` | Roll back everything above `target`. |
| `m.Status(ctx, migs)` | `[]Status{Version, Name, Applied}`. |
| `m.Version(ctx)` | `(version, dirty, err)`. |
| `m.Force(ctx, version)` | Set version + clear dirty (recovery). |

`migrate.Load` auto-detects three on-disk formats so adopters keep history:

- golang-migrate split: `NNN_name.up.sql` / `NNN_name.down.sql`
- goose / sql-migrate annotated single file: `-- +goose Up` / `-- +goose Down`
- plain numbered single file: `NNN_name.sql` (up-only)

## WritePair: bridge generated SQL into the runner

`migrate.WritePair` writes a golang-migrate-style pair that `Load` reads back — so a generated diff drops straight into the runner an adopter already uses.

```go
up, down, _ := orm.GenerateMigration[User](ctx, sess)
upPath, downPath, err := migrate.WritePair("migrations", 2, "add user fields", up, down)
// → migrations/000002_add_user_fields.up.sql  and  .down.sql
```

Version is zero-padded to six digits; the name is slugified. An empty `up` is an error; an empty `down` is written as an irreversibility comment so `Down` reports it as irreversible rather than silently succeeding.

## Pitfalls

- AutoMigrate never alters or drops — a type change or column removal needs `GenerateMigration` + the runner.
- A dirty ledger blocks all `Up`/`Down`: fix the DB by hand, then `Force` to the correct version.
- A migration with no down section is irreversible; `Down` errors instead of skipping it.

## Deeper

- Guide: [../../docs/guides/query.md](../../docs/guides/query.md)
- API: https://pkg.go.dev/liteorm.org/migrate and https://pkg.go.dev/liteorm.org/orm
