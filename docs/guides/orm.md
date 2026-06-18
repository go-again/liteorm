# The orm front-end

The `orm` package is LiteORM's declarative front-end: you describe your data as plain structs with tags, and a typed repository handles CRUD, associations, lifecycle hooks, soft deletes, and schema migration. It's convention-driven but never magical — there's no lazy loading and no silent pluralization, and an ambiguous mapping is a hard error rather than a guess.

It shares one core with the explicit [query](query.md) builder, so a value you fetch through one front-end feeds the other on the same transaction. Reach for `orm` when you want models, relations, and migrations; reach for `query` when you want to assemble SQL by hand.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/orm](https://pkg.go.dev/liteorm.org/orm).

## A model

A model is an exported struct. Give it a `TableName() string` method (otherwise the table is the snake_case of the type name), and annotate fields with `orm:"..."` tags where you need more than the defaults. `gorm:"..."` tags are also read, so models carried over from gorm work as-is.

```go
import (
	"database/sql"
	"time"

	"liteorm.org/orm"
)

type Post struct {
	ID        int64
	AuthorID  int64        `orm:"author_id"`
	Title     string
	Slug      string       `orm:"slug,unique"`
	Views     int64
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
}

func (Post) TableName() string { return "posts" }
```

The first token of an `orm` tag is the column name; the rest are options. With no tag, the column name is the snake_case of the field, and an `int64` field named `ID` is treated as an auto-increment primary key by convention.

## Tag grammar

| Tag option | Effect |
| --- | --- |
| `col_name` (first token) | override the column name |
| `pk` | primary key |
| `autoincrement` | auto-increment the key |
| `unique` | unique constraint / index on the column |
| `notnull` | `NOT NULL` |
| `default:X` | column default `X` |
| `size:N` | column size hint (e.g. varchar length) |
| `type:T` | explicit SQL column type, overriding the dialect default |
| `check:EXPR` | a `CHECK` constraint |
| `index` | index the column |
| `autocreatetime` | stamp to now on Create |
| `autoupdatetime` | stamp to now on Create and Update |
| `soft_delete` | mark the soft-delete timestamp column (see [soft delete](soft-delete.md)) |
| `readonly` | included in reads, excluded from INSERT/UPDATE |
| `writeonly` | written but excluded from SELECT column lists |
| `embedded` | flatten an embedded struct's columns into this table |
| `m2m:join_table` | many-to-many through the named junction (see [associations](associations.md)) |
| `fk:Field` | override the inferred foreign-key column |
| `references:Field` | override the referenced key column |

`autocreatetime` / `autoupdatetime` work with `time.Time`, `sql.NullTime`, or `*time.Time` fields and are stamped automatically by the repository.

## Composite primary keys

Mark more than one field `pk` and the key spans all of them, in declaration order. The table is created with a table-level `PRIMARY KEY (a, b)`, and every row operation matches on the whole key — a composite key is never auto-increment, so you assign its parts yourself.

```go
type Membership struct {
	TenantID int64 `orm:"tenant_id,pk"`
	UserID   int64 `orm:"user_id,pk"`
	Role     string
}

func (Membership) TableName() string { return "memberships" }
```

`Get` then takes one value per key column, in declaration order — `repo.Get(ctx, tenantID, userID)` — and passing the wrong number of values is a hard error rather than a partial match. `Update` and `Delete` address the row by its full key the same way. A single-column key still reads through the convenient `Schema.PK`; a composite key leaves that nil and exposes its columns via `Schema.PKs`.

## Migrating the schema

`orm.AutoMigrate[T]` brings the table for `T` into being and keeps it in sync, additively. A missing table is created with its unique indexes and any many-to-many junction tables; an existing one gains a column for anything the model added. It never drops columns or alters types — those are deliberate, reviewable changes rather than something that happens behind your back.

```go
ctx := context.Background()
db, err := sqlite.Open("blog.db")
if err != nil {
	return err
}
defer db.Close()

if err := orm.AutoMigrate[Author](ctx, db); err != nil {
	return err
}
if err := orm.AutoMigrate[Post](ctx, db); err != nil {
	return err
}
```

To migrate a whole set of models in one call, `AutoMigrateAll` takes the models as variadic zero values (a value or pointer both work) and migrates them in the order given — list a referenced table before the table that points at it:

```go
if err := orm.AutoMigrateAll(ctx, db, Author{}, Post{}, Comment{}); err != nil {
	return err
}
```

(The per-model `AutoMigrate[T]` is the form that takes options like `WithForeignKeys`; `AutoMigrateAll` is the no-options one-liner for the common case.)

For destructive or reviewable schema changes, see [migrations](migrations.md).

## The Repo: CRUD

`orm.NewRepo[T](sess)` is the typed repository. It runs against a `*liteorm.DB` or a transaction (see [transactions](transactions.md)).

```go
posts := orm.NewRepo[Post](db)

// Create: fires Before/AfterCreate hooks, stamps auto timestamps, reads the
// generated primary key back into v.
p := Post{AuthorID: ada.ID, Title: "Generics in Go"}
err := posts.Create(ctx, &p)
fmt.Println(p.ID, p.Slug, p.CreatedAt)

// Get by primary key (→ liteorm.ErrNoRows when absent or soft-deleted). For a
// composite key, pass one value per column: posts.Get(ctx, tenantID, slug).
got, err := posts.Get(ctx, p.ID)

// GetByKeys: fetch many rows by a list of primary keys in one query.
some, err := posts.GetByKeys(ctx, id1, id2, id3)

// Find all rows (honoring the soft-delete scope).
all, err := posts.Find(ctx)

// Update non-key columns; fires Before/AfterUpdate, bumps autoupdatetime.
p.Views = 42
err = posts.Update(ctx, &p)

// Delete: a soft delete when the model has a soft_delete column, else a hard
// delete. Always scoped by primary key. Fires Before/AfterDelete.
err = posts.Delete(ctx, &p)
```

A keyed `Update` or `Delete` that matches no row — a primary key that isn't there, or a soft-deleted row that's out of the current scope — returns `liteorm.ErrNoRows` rather than silently succeeding, so a no-op write is something you can detect (and the After hook does not fire). To update or delete a soft-deleted row on purpose, reach it with `IncludeDeleted()` first.

`Create`, `Update`, and `Delete` fire [lifecycle hooks](hooks.md) when your model implements them, and an error from a hook aborts the operation.

## Write conveniences

On top of the core verbs, the Repo carries the ergonomic write helpers you reach for most, each composed from the hook-firing primitives above:

```go
// Save: insert when the primary key is zero, update otherwise — upsert by identity.
err := posts.Save(ctx, &p)

// Upsert: INSERT ... ON CONFLICT DO UPDATE in one statement. Name the conflict
// column(s) and which columns to update (narrow them to preserve e.g. created_at).
err = posts.Upsert(ctx, &p, query.OnConflict("slug").DoUpdate("title", "body"))

// FirstOrCreate: load the first row matching the conditions, or insert v if none
// exists. created reports which path it took; the conditions are the lookup, v
// supplies the new row.
created, err := posts.FirstOrCreate(ctx, &p, query.Col[string]("slug").Eq("hello"))

// FirstOrInit: the non-persisting sibling — load the match, or leave v as the
// defaults you set and write nothing. found reports whether a row was loaded.
found, err := posts.FirstOrInit(ctx, &p, query.Col[string]("slug").Eq("hello"))

// Updates: write only the named columns (matched by column or Go field name).
err = posts.Updates(ctx, &p, "title", "body")

// CreateInBatches: insert many rows in chunks of N, one multi-row INSERT per
// chunk, firing per-row hooks and reading generated keys back into each element.
err = posts.CreateInBatches(ctx, []*Post{&p1, &p2, &p3}, 100)
```

`Select` and `Omit` return a scoped Repo view that narrows which columns a write touches, so a single struct can drive a partial write without zeroing the columns you leave out:

```go
// only title and body are written; everything else on the row is left as-is
err = posts.Select("title", "body").Update(ctx, &p)

// write every writable column except internal_notes
err = posts.Omit("internal_notes").Update(ctx, &p)
```

The primary key and auto-timestamp columns are still managed for you under `Select`/`Omit`; the scope governs the ordinary data columns. The soft-delete scope still applies, so `Save`/`Updates` can't silently resurrect a row that's out of scope — a keyed write matching no in-scope row returns `liteorm.ErrNoRows`.

## Filtered reads and scopes

`Find` isn't all-or-nothing. The Repo carries a thin read surface — `Where`, `Filter`, `OrderBy`, `Limit`, `Offset` — that composes onto the query builder, plus the finishers `First`, `Count`, and `Exists`. Each returns a Repo view, so you chain them and the soft-delete scope still applies underneath:

```go
recent, _ := posts.Where("views > ?", 100).OrderBy("created_at DESC").Limit(10).Find(ctx)
top, _    := posts.OrderBy("views DESC").First(ctx)
n, _      := posts.Where("author_id = ?", ada.ID).Count(ctx)
any, _    := posts.Filter(query.Col[string]("slug").Eq("hello")).Exists(ctx)
```

For filters you reuse, package them as a **scope** — a function over the query builder — and pass it to `Scopes` (gorm's `Scopes`):

```go
func Published(b *query.SelectBuilder[Post]) *query.SelectBuilder[Post] {
	return b.Where("published_at IS NOT NULL")
}
func OwnedBy(id int64) orm.Scope[Post] {
	return func(b *query.SelectBuilder[Post]) *query.SelectBuilder[Post] {
		return b.Where("author_id = ?", id)
	}
}

mine, _ := posts.Scopes(Published, OwnedBy(ada.ID)).OrderBy("created_at DESC").Find(ctx)
```

This keeps `orm.Repo` the convenience layer: it *composes* the [`query`](query.md) builder rather than forking it. When you need joins, unions, projections, or grouping, build a `query.Select[T]` on the same Session directly.

For a large result set you don't want to hold in memory at once, `FindInBatches` walks it in keyset-ordered chunks, calling your function once per batch — it honors the same scopes and the soft-delete filter, requires a single-column primary key, and stops when a batch is short or your function returns an error:

```go
err := posts.Where("views > ?", 0).FindInBatches(ctx, 500, func(batch []Post) error {
	for i := range batch {
		// process batch[i] …
	}
	return nil
})
```

(For row-at-a-time streaming instead, range over `query.Select[Post](db).Iter(ctx)` — an `iter.Seq2[Post, error]`.)

## Conventions, made explicit

LiteORM's declarative layer leans on convention, but every convention is the kind you can reason about:

- **No silent pluralization.** By default the table name is your `TableName()` or the snake_case of the type — `Post` maps to `post`, not `posts`. Want gorm-style plurals? Opt in once with `orm.UsePluralTableNames(true)` (`Post` → `posts`) and register any irregulars with `orm.RegisterPlural`; a per-type `TableName()` still wins over both.
- **No lazy loading.** Associations are never fetched implicitly when you touch a field. You load them with one explicit, N+1-safe call — see [associations](associations.md).
- **Hard errors over silent guesses.** A foreign key that can't be inferred, an unknown column, an ambiguous relation — these are returned errors, not best-effort SQL.
- **Soft delete is opt-in and scoped.** A `soft_delete` field turns deletes into updates and excludes deleted rows from reads by default, with explicit scopes to see them — see [soft delete](soft-delete.md).

## Working with associations

Define has-many, has-one, belongs-to, and many-to-many relations as struct fields, then load them explicitly. The full treatment, including the foreign-key inference rules and `m2m` setup, is in [associations](associations.md).

## Hooks

Implement typed, context-first lifecycle methods on `*T` (`BeforeCreate`, `AfterUpdate`, and so on) to run logic around writes. They're compile-checked against your model type, so a wrong signature is a build error, not a silently-dead hook. See [hooks](hooks.md).

## Where to next

- [Associations](associations.md) — has-many, has-one, belongs-to, many-to-many, eager loading.
- [Hooks](hooks.md) — lifecycle callbacks.
- [Soft delete](soft-delete.md) — soft deletes and scopes.
- [Migrations](migrations.md) — reviewable schema changes.
- [The query front-end](query.md) — the explicit builder these models also work with.
