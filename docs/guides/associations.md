# Associations

LiteORM's [orm front-end](orm.md) models four kinds of association — has-many, has-one, belongs-to, and many-to-many (plus polymorphic ownership as a variant of has-many / has-one) — as struct fields, and loads them with one explicit, batched call. There is no lazy loading: you eager-load a relation when you want it, or you simply don't have the data. That trade buys you predictable, N+1-safe queries — loading a relation is always exactly one query, never one-per-parent.

For exhaustive API detail, see the reference at [pkg.go.dev/liteorm.org/orm](https://pkg.go.dev/liteorm.org/orm).

## Declaring relations

You declare a relation by giving a model a field whose type is another model (or a slice of one). The kind is inferred from the shape, and the foreign key is inferred by convention — with a hard error if it can't be found, so a typo never becomes silent wrong SQL.

```go
type Author struct {
	ID    int64
	Name  string
	Email string `orm:"email,unique"`
	Posts []Post // has-many: the FK author_id lives on Post
}

func (Author) TableName() string { return "authors" }

type Post struct {
	ID       int64
	AuthorID int64 `orm:"author_id"`
	Title    string
	Slug     string    `orm:"slug,unique"`
	Author   *Author   // belongs-to: the FK author_id lives on Post
	Tags     []Tag     `orm:"m2m:post_tags"` // many-to-many via post_tags
	Comments []Comment // has-many: the FK post_id lives on Comment
}

func (Post) TableName() string { return "posts" }
```

### Has-many

A slice field whose element is another model: `Author.Posts []Post`. The foreign key lives on the *target* (`post.author_id`) and references the owner's primary key. By convention the key is `<owner_type>_id` — here `author_id`. If that column doesn't exist on the target, `Load` returns an error telling you to add it or set `orm:"fk:<col>"`.

### Belongs-to

A single-model field (often a pointer): `Post.Author *Author`. The foreign key lives on the *owner* (`post.author_id`) and references the target's primary key. The same `<target_type>_id` convention applies, overridable with `fk` / `references`.

### Has-one

Also a single-model field (`User.Profile *Profile`), but the foreign key lives on the *target* (`profile.user_id`) and references the owner's primary key — the mirror image of belongs-to. By convention the key is `<owner_type>_id`.

Has-one and belongs-to have the same Go shape (a non-slice struct or pointer), so LiteORM tells them apart by **where the foreign key is**: it looks for the key on the owner first (belongs-to), and falls through to the target (has-one) when the owner doesn't carry it — the same owner-first resolution gorm uses. If neither side has the key, you get a hard error that names both columns it looked for. Override the column with `fk` (and the referenced key with `references`); the override is read against whichever side owns the key.

### Many-to-many

A slice field tagged with the junction table: `Post.Tags []Tag \`orm:"m2m:post_tags"\``. The junction links the two primary keys; by convention its columns are `<owner_type>_id` and `<target_type>_id` (`post_id`, `tag_id`). `AutoMigrate` creates the junction table for you when it doesn't exist.

### Polymorphic (has-many / has-one)

When one table is owned by *several* owner types — `Toy` rows that belong to a `User` or a `Pet` — tag the relation `polymorphic`. The target carries two columns instead of one: an owner id and an owner *type*, so a single query can tell which kind of owner a row belongs to.

```go
type Toy struct {
	ID        int64
	Name      string
	OwnerID   sql.NullInt64 `orm:"owner_id"`   // nullable: detaching nulls it
	OwnerType string        `orm:"owner_type"` // "users" or "pets"
}

type User struct {
	ID   int64
	Toys []Toy `orm:"polymorphic:Owner"` // toys.owner_id + toys.owner_type
}

type Pet struct {
	ID   int64
	Toys []Toy `orm:"polymorphic:Owner"`
}
```

`polymorphic:Owner` derives the two columns from the `Owner` prefix — `owner_id` and `owner_type` — and the type value written for each owner defaults to that owner's table name (`users`, `pets`). Override the columns with `polymorphicId` / `polymorphicType` and the constant with `polymorphicValue`; the gorm spellings are read too. Loading, `Count`, and the `Assoc` writes are all scoped to the owner type automatically, so a user's `Load` never sees a pet's toys. A slice field is polymorphic has-many; a single struct/pointer field is polymorphic has-one.

This is the forward direction (owner → its polymorphic children), which covers the common case. The inverse — a `Toy.Owner` field resolving back to *either* a `User` or a `Pet` at runtime — is out of scope: LiteORM's generics-first, no-runtime-dispatch design doesn't model it cleanly. Load the owner explicitly by its concrete type.

### Overriding the inferred keys

When your columns don't follow the convention, override per relation with tags:

- `orm:"fk:<col>"` — the foreign-key column.
- `orm:"references:<col>"` — the referenced (usually primary-key) column.

The values may be column names or Go field names. `gorm` `foreignKey` / `references` / `many2many` tags are read too.

## Loading a relation

`orm.Load[Parent, Child](ctx, sess, parents, fieldName)` eager-loads one relation for a slice of parents in a single batched query, then assigns the results back onto each parent's field. It's N+1-safe by construction: one `Load` call is one query regardless of how many parents you pass.

```go
posts := orm.NewRepo[Post](db)
all, _ := posts.Find(ctx)

orm.Load[Post, Author](ctx, db, all, "Author")    // belongs-to: one IN query on author ids
orm.Load[Post, Tag](ctx, db, all, "Tags")         // many-to-many: one JOIN query
orm.Load[Post, Comment](ctx, db, all, "Comments") // has-many: one IN query on post ids

for _, p := range all {
	fmt.Printf("%q by %s — %d tags, %d comments\n",
		p.Title, p.Author.Name, len(p.Tags), len(p.Comments))
}
```

`fieldName` is the Go field name on the parent (`"Author"`, `"Tags"`, `"Comments"`), and the two type parameters are the parent and the child model — the child must match the relation's target type, or `Load` returns an error.

It works in either direction — to load an author's posts, make the author the parent:

```go
authors := []Author{ada}
orm.Load[Author, Post](ctx, db, authors, "Posts")
fmt.Printf("Ada has %d posts\n", len(authors[0].Posts))
```

To narrow or order the loaded children, pass `orm.LoadWhere` / `orm.LoadOrderBy` — the filter and order apply to the single batched query, so it stays N+1-safe:

```go
// each author's published posts, newest first — still one query
orm.Load[Author, Post](ctx, db, authors, "Posts",
	orm.LoadWhere("published_at IS NOT NULL"),
	orm.LoadOrderBy("published_at DESC"))
```

These options are for the foreign-key relations (has-many, has-one, belongs-to); filtering a many-to-many load is a clear error for now. They constrain the one fetch — they do not impose a per-parent limit (a true "top N per parent" needs a window/lateral query, which LiteORM doesn't generate for eager loads yet).

## Nested loading

`orm.LoadPath[Root](ctx, sess, roots, "Author.Company")` walks a dotted relation path, running exactly one batched query per segment — so a two-level path is two queries total, never N+1. Each segment is a Go relation field name on the type the previous segment produced.

```go
posts := orm.NewRepo[Post](db)
all, _ := posts.Find(ctx)

// Each post's author, then each of those authors' company — two queries.
orm.LoadPath[Post](ctx, db, all, "Author.Company")
```

To plan several paths at once, use the fluent `Preloader` — each path still costs one query per segment:

```go
orm.NewPreloader[Post](db).
	With("Author").
	With("Tags").
	With("Comments.Author").
	Load(ctx, all)
```

A self-referential relation may repeat in a path for a bounded-depth tree load — the depth is exactly the number of segments, so there's no unbounded recursion:

```go
// load a category, its children, and its grandchildren — three queries
orm.LoadPath[Category](ctx, db, roots, "Children.Children")
```

When you'd rather drive the levels yourself, chain single `Load` calls off the slice each level produces; the cost is identical (one query per level).

## Writing associations

`orm.Assoc[Owner, Target](sess, fieldName, &owner)` opens a typed write handle over any relation whose foreign key is *not* on the owner — has-many, has-one, or many-to-many — with the operations you'd reach for: `Append`, `Delete`, `Replace`, `Clear`, and `Count`. Create the rows on both sides first (so they have primary keys); the handle links them, it never cascade-saves a graph.

```go
tags := orm.NewRepo[Tag](db)
goTag := Tag{Name: "golang"}
dbTag := Tag{Name: "databases"}
tags.Create(ctx, &goTag)
tags.Create(ctx, &dbTag)

rel, _ := orm.Assoc[Post, Tag](db, "Tags", &p1)
rel.Append(ctx, &goTag, &dbTag) // link both tags to p1
n, _ := rel.Count(ctx)          // 2
rel.Delete(ctx, &goTag)         // unlink one
rel.Replace(ctx, &dbTag)        // the set becomes exactly {dbTag}
rel.Clear(ctx)                  // unlink all
```

The relation kinds behave as you'd expect:

- **Many-to-many** — `Append` inserts junction rows (idempotent: re-linking an existing pair is a no-op), `Delete` removes them, `Clear` removes every link for this owner. The target rows are never touched.
- **Has-many** — `Append` points each target's foreign key at the owner; `Delete` and `Clear` *detach* by setting that foreign key back to `NULL` (so the column must be nullable). They never delete target rows — removing the rows themselves is a `Repo.Delete`, stated explicitly.
- **Has-one** — the same foreign-key path as has-many, but to-one: `Replace(target)` is the natural setter (it detaches the previous target and points the new one at the owner), `Clear` detaches it, and `Count` is 0 or 1.
- **Polymorphic has-many / has-one** — as above, but `Append` also stamps the owner-type column, and `Delete` / `Clear` / `Count` are scoped to it, so writes for one owner type never touch another's rows.

`Append` writes the foreign key back into the in-memory target structs for has-many and has-one. To refresh the owner's field after any write, call `orm.Load` with the same field name. Belongs-to is a single foreign key on the owner, not managed here — set that field and `Update` the owner instead; `Assoc` returns an error for it.

`orm.Attach` / `orm.Detach` remain as the lower-level many-to-many link/unlink primitives (`Append` / `Delete` are built on them); reach for `Assoc` for the full surface.

## Where to next

- [The orm front-end](orm.md) — models, tags, the repository.
- [Hooks](hooks.md) — run logic around writes that touch related data.
- [Transactions](transactions.md) — attach links and create related rows atomically.
