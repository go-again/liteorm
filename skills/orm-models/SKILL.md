---
name: orm-models
description: Use when working with liteorm's declarative orm front-end — model structs and tags, AutoMigrate, the orm.Repo, associations (Load/Attach), hooks, or soft delete.
---

# orm models

Import `liteorm.org/orm`. A model is a struct with a `TableName() string` method and `orm:"..."` tags. Gorm `gorm:"..."` tags are read natively too, so existing gorm models work unchanged.

## Define a model

```go
type Post struct {
    ID        int64
    AuthorID  int64        `orm:"author_id"`
    Title     string
    Slug      string       `orm:"slug,unique"`
    CreatedAt time.Time    `orm:"created_at,autocreatetime"`
    UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
    DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
    Author    *Author      // belongs-to: FK author_id on Post (owner)
    Tags      []Tag        `orm:"m2m:post_tags"` // many-to-many via post_tags
    Comments  []Comment    // has-many: FK post_id on Comment (target)
    Meta      *PostMeta    // has-one: FK post_id on PostMeta (target)
}
func (Post) TableName() string { return "posts" }
```

Relation kinds, inferred from shape + where the FK is: **slice** → has-many (or `m2m:` → many-to-many); **non-slice struct/pointer** → belongs-to when the *owner* has the FK (`<target>_id`), else has-one when the *target* has it (`<owner>_id`). Belongs-to is tried first (owner-first, like gorm); a real miss is a hard error naming both columns. Override with `fk:`/`references:` (read against whichever side owns the key).

**Polymorphic** (`orm:"polymorphic:Owner"` on a slice → has-many, on a struct/pointer → has-one): the target carries `owner_id` + `owner_type` columns (derived from the `Owner` prefix), so one table is owned by several owner types. The type value defaults to the owner's table name; override columns with `polymorphicid:`/`polymorphictype:` and the constant with `polymorphicvalue:` (gorm spellings read too). Load/Count/Assoc are all auto-scoped to the owner type. Make `owner_id` `sql.NullInt64` so detach can null it. Forward-only — the inverse (`Toy.Owner` → User *or* Pet at runtime) is out of scope.

The tag is `column_name,opt,opt`. Common opts: `pk`, `autoincrement`, `unique`, `notnull`, `default:VALUE`, `type:DDL`, `size:N`, `check:EXPR`, `index`, `autocreatetime`, `autoupdatetime`, `soft_delete`, `m2m:join_table`, `fk:col`, `references:col`, `embedded`, `embeddedprefix:p`, `-` (skip). A bare `int64 ID` with no tag is the auto-increment PK by convention.

**Composite primary key**: tag more than one field `pk` and the key spans all of them in declaration order (table-level `PRIMARY KEY (a, b)`, never auto-increment — you assign the parts). `Get`/`Update`/`Delete` then address a row by its whole key; `Get(ctx, a, b)` takes one value per column and a wrong count is a hard error. `Schema.PK` is the single-PK convenience (nil for composite); `Schema.PKs` is always the full ordered list.

## AutoMigrate (additive only)

```go
_ = orm.AutoMigrate[Author](ctx, sess)
_ = orm.AutoMigrate[Post](ctx, sess)   // also creates the post_tags junction table

// One-liner for a whole set (variadic zero values, migrated in the given order —
// referenced tables first). The options form is AutoMigrate[T]; this is no-options.
_ = orm.AutoMigrateAll(ctx, sess, Author{}, Post{}, Comment{})
```

Creates a missing table (with unique indexes + m2m junctions); syncs an existing one by `ADD COLUMN` only. It never drops or alters types — those are a reviewable migration (see the migrations skill). Unique indexes on soft-delete models are partial (`WHERE deleted_at IS NULL`) so a soft-deleted row frees its unique key.

## Repo

```go
repo := orm.NewRepo[Post](sess)
```

| Method | Notes |
| --- | --- |
| `Create(ctx, *v)` | Insert; fires Before/AfterCreate; stamps autoCreate/autoUpdate times; PK read back. |
| `Get(ctx, id)` | By PK, honoring the soft-delete scope; `ErrNoRows` if missing. Composite key: one value per column, `Get(ctx, a, b)`. |
| `GetByKeys(ctx, keys...)` | Batch get by a list of primary keys (one `WHERE pk IN (...)`); honors the soft-delete scope; single-PK only. |
| `Find(ctx)` | All rows in the current scope. |
| `Update(ctx, *v)` | Non-key columns by PK; fires Before/AfterUpdate; bumps autoUpdate time. Returns `ErrNoRows` if no row matches (wrong PK, or out-of-scope soft-deleted). |
| `Save(ctx, *v)` | Insert when the PK is zero, else Update — upsert by identity; fires the matching hooks. |
| `Upsert(ctx, *v, query.OnConflict(...).DoUpdate(...))` | INSERT … ON CONFLICT DO UPDATE in one statement; fires Before/AfterCreate; narrow DoUpdate cols to preserve e.g. created_at. |
| `FirstOrCreate(ctx, *v, conds...)` | Load the first row matching `conds`, or Create `v` if none; returns `created bool`. |
| `FirstOrInit(ctx, *v, conds...)` | Load the first row matching `conds`, or leave `*v` as its defaults and write nothing; returns `found bool` (non-persisting sibling of FirstOrCreate). |
| `FindInBatches(ctx, n, fn)` | Process matching rows in keyset chunks of `n` (calls `fn(batch []T)`); honors scopes + soft-delete; single-PK only; stops on short batch or `fn` error. |
| `Updates(ctx, *v, cols...)` | Partial update: write only the named columns (column or Go field name). No cols → full Update. |
| `CreateInBatches(ctx, []*T, n)` | Insert in chunks of `n` (one multi-row INSERT each); per-row hooks; keys read back on RETURNING/OUTPUT dialects. |
| `Delete(ctx, *v)` | Soft delete (sets `deleted_at`) if the model has a soft-delete column, else hard DELETE. Fires Before/AfterDelete. Returns `ErrNoRows` if no row matches. |
| `ForceDelete(ctx, *v)` | Always a hard DELETE. |
| `Restore(ctx, *v)` | Un-soft-delete: clears `deleted_at` for the row by PK (reaches the deleted row), fires Before/AfterUpdate; `ErrNoRows` if none; errors without a soft-delete column. |
| `Select(cols...)` / `Omit(cols...)` | Return a Repo view whose writes touch only / never the named columns (PK + auto-times still managed). |
| `Where/Filter/OrderBy/Limit/Offset` | Compose a read scope onto Find/First/Count/Exists (delegates to the query builder). |
| `First/Count/Exists(ctx)` | Read finishers honoring the composed + soft-delete scopes. |
| `Scopes(scopes...)` | Apply reusable `orm.Scope[T]` filters (gorm Scopes). |
| `IncludeDeleted()` / `OnlyDeleted()` | Return a Repo view with that read scope (chain before Get/Find). |

```go
live, _    := repo.Find(ctx)                  // excludes soft-deleted (default)
withDel, _ := repo.IncludeDeleted().Find(ctx)
onlyDel, _ := repo.OnlyDeleted().Find(ctx)

recent, _ := repo.Where("views > ?", 100).OrderBy("created_at DESC").Limit(10).Find(ctx)
n, _      := repo.Where("author_id = ?", id).Count(ctx)
// reusable scope: func(b *query.SelectBuilder[Post]) *query.SelectBuilder[Post]
mine, _   := repo.Scopes(Published, OwnedBy(id)).Find(ctx)

repo.Save(ctx, &p)                            // insert-or-update by PK
created, _ := repo.FirstOrCreate(ctx, &p, query.Col[string]("slug").Eq("hello"))
repo.Updates(ctx, &p, "title", "body")        // write only these columns
repo.Omit("internal_notes").Update(ctx, &p)   // write everything except this
```

## Associations — explicit eager load (N+1-safe, NO lazy load)

There is no lazy loading. You load a relation explicitly with `orm.Load[Parent, Child]`, which runs exactly ONE batched query per call regardless of parent count.

```go
posts, _ := repo.Find(ctx)
_ = orm.Load[Post, Author](ctx, sess, posts, "Author")    // belongs-to
_ = orm.Load[Post, Tag](ctx, sess, posts, "Tags")         // many-to-many
_ = orm.Load[Post, Comment](ctx, sess, posts, "Comments") // has-many
_ = orm.Load[Post, PostMeta](ctx, sess, posts, "Meta")    // has-one
// now posts[i].Author / .Tags / .Comments are populated
```

Filter/order the loaded children with `orm.LoadWhere("...", args...)` / `orm.LoadOrderBy("...")` (raw fragments, still one query) — for the FK relations (has-many/has-one/belongs-to); filtering a many-to-many load errors. They don't impose a per-parent limit (top-N-per-parent needs a window/lateral query, not generated yet).

The field name is the Go struct field. For nested eager loads, walk a dotted path with `orm.LoadPath` — one batched query per segment, still N+1-safe:

```go
_ = orm.LoadPath[Post](ctx, sess, posts, "Author.Company")     // two levels, two queries
_ = orm.NewPreloader[Post](sess).With("Tags").With("Comments.Author").Load(ctx, posts)
_ = orm.LoadPath[Category](ctx, sess, roots, "Children.Children") // bounded-depth tree
```

A self-referential segment may repeat; depth equals the number of segments (no unbounded recursion). An unknown segment is a hard error.

Write a relation whose FK is not on the owner (has-many, has-one, or many-to-many) with the `orm.Assoc` handle — never cascade-saves, links by existing PKs:

```go
rel, _ := orm.Assoc[Post, Tag](sess, "Tags", &post)
_ = rel.Append(ctx, &goTag, &dbTag) // m2m: insert junction rows (idempotent)
n, _ := rel.Count(ctx)
_ = rel.Delete(ctx, &goTag)         // m2m: remove links; has-many: null the FK
_ = rel.Replace(ctx, &dbTag)        // set becomes exactly the args
_ = rel.Clear(ctx)
```

For has-many/has-one, `Append` sets each target's FK to the owner; `Delete`/`Clear` detach by nulling the FK (column must be nullable), never deleting target rows. For has-one (to-one) `Replace` is the natural setter. Belongs-to is a single FK on the owner — set the field and `Update` the owner; `Assoc` errors on it. For a **polymorphic** relation, `Append` also stamps `owner_type` and `Delete`/`Clear`/`Count` are scoped to it (one owner type never touches another's rows). `orm.Attach`/`orm.Detach` are the lower-level m2m link/unlink primitives `Append`/`Delete` build on.

## Hooks

Implement the hook method on `*T`; a wrong signature is a compile error, not a dead hook. The `*orm.Event[T]` carries `Sess` (the executing session) and `Model` (`*T`). Returning an error aborts the operation.

```go
func (p *Post) BeforeCreate(ctx context.Context, ev *orm.Event[Post]) error {
    if ev.Model.Slug == "" { ev.Model.Slug = slugify(ev.Model.Title) }
    return nil
}
var _ orm.BeforeCreateHook[Post] = (*Post)(nil) // optional compile-time assert
```

Available: `BeforeCreate` / `AfterCreate`, `BeforeUpdate` / `AfterUpdate`, `BeforeDelete` / `AfterDelete` — each `(ctx, *orm.Event[T]) error`.

## Soft delete

Declare a `sql.NullTime` field with the `soft_delete` tag:

```go
DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
```

Then `Delete` soft-deletes, reads exclude deleted rows by default, and `IncludeDeleted()`/`OnlyDeleted()`/`ForceDelete` give the explicit opt-outs.

## Pitfalls

- No lazy loading: accessing `post.Comments` without first calling `orm.Load` gives an empty/zero value, not the rows.
- A soft-delete column MUST be `sql.NullTime` with the `soft_delete` tag — a plain `time.Time` or a different type won't work.
- Default reads hide soft-deleted rows; use `IncludeDeleted()`/`OnlyDeleted()` when you need them.
- Nested eager loading uses a dotted path (`orm.LoadPath` / `Preloader.With("A.B")`), one batched query per segment — not lazy traversal. A single `orm.Load` still loads one relation level.

## Deeper

- Guide: [../../docs/guides/orm.md](../../docs/guides/orm.md) · [associations](../../docs/guides/associations.md)
- API: https://pkg.go.dev/liteorm.org/orm
