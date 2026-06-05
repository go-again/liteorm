// Command orm is a comprehensive tour of liteorm's declarative `orm` front-end on
// SQLite: AutoMigrate, associations (has-many / belongs-to / many-to-many) with
// N+1-safe eager loading, a ctx-first BeforeCreate hook, soft-delete with
// tri-state scopes and the partial-unique-index fix, autoCreate/autoUpdate
// timestamps, the diff→reviewable-migration generator, and query↔orm interop on
// one transaction. It runs against a throwaway database and cleans up after itself.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/orm"
	"liteorm.org/query"
)

type Author struct {
	ID          int64
	Name        string
	Email       string       `orm:"email,unique"`
	Posts       []Post       // has-many: FK author_id on Post
	Profile     *Profile     // has-one: FK author_id on Profile (the key is on the target)
	Attachments []Attachment `orm:"polymorphic:Owner"` // polymorphic: attachments.owner_id/owner_type
}

func (Author) TableName() string { return "authors" }

// Profile is the has-one target of Author: the foreign key author_id lives here,
// not on Author, so Author.Profile resolves to has-one rather than belongs-to.
type Profile struct {
	ID       int64
	AuthorID int64 `orm:"author_id"`
	Bio      string
}

func (Profile) TableName() string { return "profiles" }

type Tag struct {
	ID   int64
	Name string `orm:"name,unique"`
}

func (Tag) TableName() string { return "tags" }

type Comment struct {
	ID     int64
	PostID int64 `orm:"post_id"`
	Author string
	Body   string
	Post   *Post // belongs-to: FK post_id on Comment
}

func (Comment) TableName() string { return "comments" }

// PostTagRole carries a composite primary key — the (post_id, tag_id) pair —
// modeling a role assigned to a tag on a post. A composite key is never
// auto-increment; you assign every part.
type PostTagRole struct {
	PostID int64 `orm:"post_id,pk"`
	TagID  int64 `orm:"tag_id,pk"`
	Role   string
}

func (PostTagRole) TableName() string { return "post_tag_roles" }

// Attachment is owned polymorphically by a Post or an Author via
// (owner_id, owner_type): one table, several owner types. owner_id is nullable so
// detaching nulls the link without deleting the row.
type Attachment struct {
	ID        int64
	Filename  string
	OwnerID   sql.NullInt64 `orm:"owner_id"`
	OwnerType string        `orm:"owner_type"`
}

func (Attachment) TableName() string { return "attachments" }

// inventoryV1 / inventoryV2 demonstrate reviewable type-change detection: the qty
// column goes from text to int64 between the two model versions.
type inventoryV1 struct {
	ID  int64
	Qty string
}

func (inventoryV1) TableName() string { return "inventory" }

type inventoryV2 struct {
	ID  int64
	Qty int64
}

func (inventoryV2) TableName() string { return "inventory" }

type Post struct {
	ID          int64
	AuthorID    int64 `orm:"author_id"`
	Title       string
	Slug        string `orm:"slug,unique"`
	Views       int64
	CreatedAt   time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt   time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt   sql.NullTime `orm:"deleted_at,soft_delete"`
	Author      *Author      // belongs-to: FK author_id on Post
	Tags        []Tag        `orm:"m2m:post_tags"` // many-to-many via post_tags
	Comments    []Comment    // has-many: FK post_id on Comment
	Attachments []Attachment `orm:"polymorphic:Owner"` // polymorphic: shared with Author
}

func (Post) TableName() string { return "posts" }

// BeforeCreate is a ctx-first hook: derive the slug from the title when unset.
func (p *Post) BeforeCreate(_ context.Context, op *orm.Op[Post]) error {
	if op.Model.Slug == "" {
		op.Model.Slug = slugify(op.Model.Title)
	}
	return nil
}

var _ orm.BeforeCreateHook[Post] = (*Post)(nil)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }

func run() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "liteorm-orm-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	db, err := sqlite.Open(filepath.Join(dir, "blog.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	// ---- AutoMigrate: tables + the post_tags junction + partial unique indexes ----
	section("AutoMigrate")
	if err := orm.AutoMigrateAll(ctx, db,
		Author{}, Profile{}, Tag{}, Post{}, Comment{}, PostTagRole{}, Attachment{}); err != nil {
		return err
	}
	fmt.Println("created authors, profiles, tags, posts, comments (+ post_tags junction)")

	authors := orm.NewRepo[Author](db)
	posts := orm.NewRepo[Post](db)
	tags := orm.NewRepo[Tag](db)
	comments := orm.NewRepo[Comment](db)

	// ---- Create with hook (slug) + RETURNING id + auto timestamps ----
	section("Create (hook + auto timestamps)")
	ada := Author{Name: "Ada", Email: "ada@x.io"}
	grace := Author{Name: "Grace", Email: "grace@x.io"}
	_ = authors.Create(ctx, &ada)
	_ = authors.Create(ctx, &grace)

	p1 := Post{AuthorID: ada.ID, Title: "Generics in Go"}
	_ = posts.Create(ctx, &p1) // hook fills slug; created_at/updated_at stamped
	p2 := Post{AuthorID: ada.ID, Title: "Iterators are nice"}
	_ = posts.Create(ctx, &p2)
	p3 := Post{AuthorID: grace.ID, Title: "Compilers 101"}
	_ = posts.Create(ctx, &p3)
	fmt.Printf("post %d slug=%q created_at set=%v\n", p1.ID, p1.Slug, !p1.CreatedAt.IsZero())

	// ---- many-to-many: tags + the Assoc write handle ----
	section("Many-to-many (Assoc handle)")
	goTag := Tag{Name: "golang"}
	dbTag := Tag{Name: "databases"}
	eduTag := Tag{Name: "education"}
	for _, t := range []*Tag{&goTag, &dbTag, &eduTag} {
		_ = tags.Create(ctx, t)
	}
	p1Tags, _ := orm.Assoc[Post, Tag](db, "Tags", &p1)
	_ = p1Tags.Append(ctx, &goTag, &dbTag) // insert junction rows (idempotent)
	p3Tags, _ := orm.Assoc[Post, Tag](db, "Tags", &p3)
	_ = p3Tags.Append(ctx, &eduTag)
	n, _ := p1Tags.Count(ctx)
	fmt.Printf("linked tags via post_tags; p1 has %d tags\n", n)

	// ---- has-many comments ----
	_ = comments.Create(ctx, &Comment{PostID: p1.ID, Author: "bob", Body: "great post"})
	_ = comments.Create(ctx, &Comment{PostID: p1.ID, Author: "cleo", Body: "thanks!"})

	// ---- Eager loading: belongs-to + m2m + has-many, all N+1-safe ----
	section("Eager load (one batched query per relation)")
	all, _ := posts.Find(ctx)
	_ = orm.Load[Post, Author](ctx, db, all, "Author")    // belongs-to (IN on author id)
	_ = orm.Load[Post, Tag](ctx, db, all, "Tags")         // m2m (single JOIN)
	_ = orm.Load[Post, Comment](ctx, db, all, "Comments") // has-many (IN on post id)
	for _, p := range all {
		fmt.Printf("  %-20q by %-6s tags=%v comments=%d\n", p.Title, authorName(p.Author), tagNames(p.Tags), len(p.Comments))
	}

	// has-many the other way: an author's posts
	loaded := []Author{ada}
	_ = orm.Load[Author, Post](ctx, db, loaded, "Posts")
	fmt.Printf("  Ada has %d posts\n", len(loaded[0].Posts))

	// Nested path: each comment's post, then that post's author — one query per
	// segment, N+1-safe (the Preloader plans several paths off one slice).
	cmts, _ := comments.Find(ctx)
	_ = orm.NewPreloader[Comment](db).With("Post.Author").Load(ctx, cmts)
	for _, c := range cmts {
		if c.Post != nil && c.Post.Author != nil {
			fmt.Printf("  comment by %s → post %q by %s\n", c.Author, c.Post.Title, c.Post.Author.Name)
		}
	}

	// ---- Has-one: the FK lives on the target (profiles.author_id) ----
	section("Has-one (Author.Profile, FK on the target)")
	_ = orm.NewRepo[Profile](db).Create(ctx, &Profile{AuthorID: ada.ID, Bio: "Countess of Lovelace"})
	withProfile := []Author{ada}
	_ = orm.Load[Author, Profile](ctx, db, withProfile, "Profile") // one batched query, assigned as a single value
	if p := withProfile[0].Profile; p != nil {
		fmt.Printf("  %s's profile: %q\n", withProfile[0].Name, p.Bio)
	}

	// ---- Composite primary key: the whole (post_id, tag_id) pair keys the row ----
	section("Composite primary key (post_id, tag_id)")
	roles := orm.NewRepo[PostTagRole](db)
	_ = roles.Create(ctx, &PostTagRole{PostID: p1.ID, TagID: goTag.ID, Role: "primary"})
	_ = roles.Create(ctx, &PostTagRole{PostID: p1.ID, TagID: dbTag.ID, Role: "secondary"})
	// Get takes one value per key column, in declaration order.
	role, _ := roles.Get(ctx, p1.ID, goTag.ID)
	fmt.Printf("  role for (post %d, tag %d) = %q\n", role.PostID, role.TagID, role.Role)
	// Update/Delete address a row by its whole key.
	role.Role = "lead"
	_ = roles.Update(ctx, &role)
	_ = roles.Delete(ctx, &PostTagRole{PostID: p1.ID, TagID: dbTag.ID})
	remaining, _ := roles.Count(ctx)
	updated, _ := roles.Get(ctx, p1.ID, goTag.ID)
	fmt.Printf("  after update+delete: %q role remains, %d row(s) left\n", updated.Role, remaining)

	// ---- Polymorphic: one attachments table owned by a Post OR an Author ----
	section("Polymorphic association (owner_id, owner_type)")
	attachments := orm.NewRepo[Attachment](db)
	readme := &Attachment{Filename: "README.md"}
	diagram := &Attachment{Filename: "diagram.png"}
	avatar := &Attachment{Filename: "avatar.jpg"}
	for _, at := range []*Attachment{readme, diagram, avatar} {
		_ = attachments.Create(ctx, at)
	}
	postFiles, _ := orm.Assoc[Post, Attachment](db, "Attachments", &p1)
	_ = postFiles.Append(ctx, readme, diagram) // stamps owner_id + owner_type="posts"
	authorFiles, _ := orm.Assoc[Author, Attachment](db, "Attachments", &ada)
	_ = authorFiles.Append(ctx, avatar) // owner_type="authors"
	pf, _ := postFiles.Count(ctx)
	af, _ := authorFiles.Count(ctx)
	fmt.Printf("  p1 has %d attachments, Ada has %d (same table, scoped by owner_type)\n", pf, af)
	withFiles := []Post{p1}
	_ = orm.Load[Post, Attachment](ctx, db, withFiles, "Attachments") // only this post's files
	fmt.Printf("  loaded p1 attachments: %v\n", attachmentNames(withFiles[0].Attachments))
	_ = postFiles.Delete(ctx, diagram) // detach one (nulls owner_id/owner_type), row survives
	after, _ := postFiles.Count(ctx)
	orphan, _ := attachments.Get(ctx, diagram.ID)
	fmt.Printf("  after Delete: p1 has %d; detached %q still exists (owner_id set=%v)\n", after, orphan.Filename, orphan.OwnerID.Valid)

	// ---- Soft delete: tri-state scopes, the unique-index fix, ForceDelete ----
	section("Soft delete (tri-state + unique-index fix)")
	_ = posts.Delete(ctx, &p2) // soft delete (sets deleted_at)
	live, _ := posts.Find(ctx)
	withDel, _ := posts.IncludeDeleted().Find(ctx)
	onlyDel, _ := posts.OnlyDeleted().Find(ctx)
	fmt.Printf("  live=%d  includeDeleted=%d  onlyDeleted=%d\n", len(live), len(withDel), len(onlyDel))
	// The slug "iterators-are-nice" is freed by the soft delete (partial unique index):
	reuse := Post{AuthorID: grace.ID, Title: "Iterators are nice"} // same slug
	if err := posts.Create(ctx, &reuse); err != nil {
		fmt.Printf("  re-create with freed slug FAILED: %v\n", err)
	} else {
		fmt.Printf("  re-created freed slug %q (new post %d)\n", reuse.Slug, reuse.ID)
	}
	_ = posts.ForceDelete(ctx, &p2) // hard delete the original
	gone, _ := posts.IncludeDeleted().Find(ctx)
	fmt.Printf("  after ForceDelete, total posts (incl. deleted) = %d\n", len(gone))

	// ---- Update bumps autoUpdateTime ----
	section("Update (autoUpdateTime)")
	time.Sleep(5 * time.Millisecond)
	p1.Views = 42
	_ = posts.Update(ctx, &p1)
	fmt.Printf("  views=%d  updated_at advanced past created_at: %v\n", p1.Views, p1.UpdatedAt.After(p1.CreatedAt))

	// ---- Write conveniences: Save / FirstOrCreate / Updates / Select-Omit / batches ----
	section("Write conveniences (Save / FirstOrCreate / Updates / batches)")
	// Save upserts by identity: zero PK inserts, non-zero updates.
	fresh := Post{AuthorID: ada.ID, Title: "Saved post"}
	_ = posts.Save(ctx, &fresh) // PK zero → INSERT
	fresh.Views = 7
	_ = posts.Save(ctx, &fresh) // PK set → UPDATE
	fmt.Printf("  Save: post %d views=%d (inserted then updated)\n", fresh.ID, fresh.Views)

	// FirstOrCreate: find-by-condition or insert.
	existingAda := Author{Name: "Ada", Email: "ada@x.io"}
	created, _ := authors.FirstOrCreate(ctx, &existingAda, query.Col[string]("email").Eq("ada@x.io"))
	fmt.Printf("  FirstOrCreate(ada): created=%v (already existed)\n", created)

	// Updates / Omit: write a chosen subset of columns.
	fresh.Title, fresh.Views = "Renamed", 999
	_ = posts.Updates(ctx, &fresh, "Views")     // only views written; title untouched in DB
	_ = posts.Omit("Views").Update(ctx, &fresh) // everything except views
	reread, _ := posts.Get(ctx, fresh.ID)
	fmt.Printf("  Updates(\"Views\")+Omit(\"Views\"): title=%q views=%d\n", reread.Title, reread.Views)

	// CreateInBatches: one multi-row INSERT per chunk, keys read back.
	batch := []*Post{
		{AuthorID: grace.ID, Title: "Batch one"},
		{AuthorID: grace.ID, Title: "Batch two"},
		{AuthorID: grace.ID, Title: "Batch three"},
	}
	_ = posts.CreateInBatches(ctx, batch, 2)
	fmt.Printf("  CreateInBatches: inserted %d posts, ids %d,%d,%d\n", len(batch), batch[0].ID, batch[1].ID, batch[2].ID)

	// ---- Filtered reads + a reusable scope (composes the query builder) ----
	section("Filtered reads + reusable scope")
	byGrace, _ := posts.Where("author_id = ?", grace.ID).OrderBy("title ASC").Find(ctx)
	fmt.Printf("  Grace's posts (ordered): %v\n", postTitles(byGrace))
	total, _ := posts.Count(ctx)
	fmt.Printf("  total live posts: %d\n", total)
	authoredBy := func(id int64) orm.Scope[Post] {
		return func(b *query.SelectBuilder[Post]) *query.SelectBuilder[Post] {
			return b.Where("author_id = ?", id)
		}
	}
	mine, _ := posts.Scopes(authoredBy(ada.ID)).Count(ctx)
	fmt.Printf("  Ada's posts via Scopes: %d\n", mine)

	// ---- Two-track migration: a reviewable diff for a destructive change ----
	section("GenerateMigration (destructive change → reviewable SQL, not executed)")
	_, _ = db.ExecContext(ctx, `ALTER TABLE posts ADD COLUMN legacy_flag TEXT`) // a column the model lacks
	up, _, _ := orm.GenerateMigration[Post](ctx, db)
	for line := range strings.SplitSeq(up, "\n") {
		fmt.Printf("  %s\n", line)
	}
	if cols, _ := orm.IntrospectColumns(ctx, db, "posts"); hasCol(cols, "legacy_flag") {
		fmt.Println("  (legacy_flag still present — the generator did NOT execute it)")
	}

	// ---- A retyped column is also reviewable, never an automatic ALTER ----
	section("GenerateMigration (type change → reviewable, not executed)")
	_ = orm.AutoMigrate[inventoryV1](ctx, db) // inventory.qty starts as TEXT
	tch, _ := orm.Diff[inventoryV2](ctx, db)  // the model now declares qty int64
	if len(tch.Changed) == 1 {
		c := tch.Changed[0]
		fmt.Printf("  type change on %q: %s → %s (detected, not applied)\n", c.Column, c.From, c.To)
	}
	tyUp, _, _ := orm.GenerateMigration[inventoryV2](ctx, db)
	fmt.Printf("  %s\n", strings.TrimSpace(tyUp)) // a commented ALTER — safe to run as-is

	// ---- Interop: write via orm, read via query, on one transaction ----
	section("Interop: orm + query on one transaction")
	tx, _ := db.Begin(ctx)
	hopper := Author{Name: "Hopper", Email: "hopper@x.io"}
	_ = orm.NewRepo[Author](tx).Create(ctx, &hopper)
	viaQuery, _ := query.Select[Author](tx).Filter(query.Col[int64]("id").Eq(hopper.ID)).All(ctx)
	fmt.Printf("  orm wrote author %d; query read it back on the same tx: %q\n", hopper.ID, viaQuery[0].Name)
	_ = tx.Commit(ctx)

	fmt.Println()
	return nil
}

func slugify(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

func authorName(a *Author) string {
	if a == nil {
		return "?"
	}
	return a.Name
}

func tagNames(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func postTitles(ps []Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func attachmentNames(as []Attachment) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Filename
	}
	return out
}

func hasCol(cols []orm.ColumnMeta, name string) bool {
	for _, c := range cols {
		if c.Name == name {
			return true
		}
	}
	return false
}
