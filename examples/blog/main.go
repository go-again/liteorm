// Command blog is a small but complete blog engine built on liteorm's declarative
// orm front-end over SQLite (via gosqlite.org). Rather than a feature checklist
// (see examples/orm and examples/query for those), it reads like a real app:
// model authors, posts, tags, and comments; migrate; seed; then render a homepage
// and a post page, answer an editorial question, and let a reader comment — using
// associations, nested eager loading, filtered reads, an aggregate query, and a
// transaction the way you actually would.
//
// Set BLOG_DEBUG=1 to watch every executed SQL statement, traced back to the line
// of this file that issued it, via the colored dev logger (see liteorm.org/log).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	devlog "liteorm.org/log"
	"liteorm.org/orm"
	"liteorm.org/query"
)

type Author struct {
	ID    int64
	Name  string
	Email string `orm:"email,unique"`
	Bio   string
}

func (Author) TableName() string { return "authors" }

type Tag struct {
	ID   int64
	Name string `orm:"name,unique"`
}

func (Tag) TableName() string { return "tags" }

type Post struct {
	ID        int64
	AuthorID  int64 `orm:"author_id"`
	Title     string
	Slug      string `orm:"slug,unique"`
	Body      string
	Published bool
	CreatedAt time.Time `orm:"created_at,autocreatetime"`
	Author    *Author   // belongs-to: FK author_id
	Tags      []Tag     `orm:"m2m:post_tags"` // many-to-many via post_tags
	Comments  []Comment // has-many: FK post_id on Comment
}

func (Post) TableName() string { return "posts" }

// BeforeCreate derives the URL slug from the title when one isn't set.
func (p *Post) BeforeCreate(_ context.Context, op *orm.Op[Post]) error {
	if op.Model.Slug == "" {
		op.Model.Slug = slugify(op.Model.Title)
	}
	return nil
}

var _ orm.BeforeCreateHook[Post] = (*Post)(nil)

type Comment struct {
	ID        int64
	PostID    int64 `orm:"post_id"`
	Author    string
	Body      string
	CreatedAt time.Time `orm:"created_at,autocreatetime"`
}

func (Comment) TableName() string { return "comments" }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "liteorm-blog-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	var opts []liteorm.Option
	if os.Getenv("BLOG_DEBUG") != "" { // watch every statement, traced to this file
		opts = append(opts, liteorm.WithLogger(devlog.New(os.Stderr, nil)))
	}
	db, err := sqlite.Open(filepath.Join(dir, "blog.db"), opts...)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Println("══ liteorm blog ══")
	if err := migrate(ctx, db); err != nil {
		return err
	}
	if err := seed(ctx, db); err != nil {
		return err
	}

	posts := orm.NewRepo[Post](db)

	// Homepage: published posts, newest first, each with its author, tags, and
	// comment count. The filtered read is the orm convenience layer; the Preloader
	// loads the three relations in one batched query apiece — no N+1.
	section("Homepage (published, newest first)")
	home, err := posts.Where("published = ?", true).OrderBy("created_at DESC, id DESC").Find(ctx)
	if err != nil {
		return err
	}
	if err := orm.NewPreloader[Post](db).With("Author").With("Tags").With("Comments").Load(ctx, home); err != nil {
		return err
	}
	for _, p := range home {
		fmt.Printf("  %-24q by %-13s %-14s · %s\n",
			p.Title, authorName(p.Author), hashtags(p.Tags), countOf(len(p.Comments), "comment"))
	}

	// Post page: one post by its slug, with author, tags, and comments.
	section("Post page: /generics-in-go")
	post, err := posts.Where("slug = ?", "generics-in-go").First(ctx)
	if err != nil {
		return err
	}
	page := []Post{post}
	if err := orm.NewPreloader[Post](db).With("Author").With("Tags").With("Comments").Load(ctx, page); err != nil {
		return err
	}
	post = page[0]
	fmt.Printf("  %s — by %s\n  tags: %s\n", post.Title, authorName(post.Author), strings.Join(tagNames(post.Tags), ", "))
	fmt.Printf("  comments (%d):\n", len(post.Comments))
	for _, c := range post.Comments {
		fmt.Printf("    • %s: %s\n", c.Author, c.Body)
	}

	// An editorial question — "what's generating discussion?" — is a join + group
	// the query front-end answers directly, scanning into a typed result.
	section("Most discussed (LEFT JOIN + GROUP BY via query.Raw)")
	type discussed struct {
		Title    string `db:"title"`
		Comments int64  `db:"comments"`
	}
	stats, err := query.Raw[discussed](ctx, db,
		`SELECT p.title AS title, count(c.id) AS comments
		   FROM posts p LEFT JOIN comments c ON c.post_id = p.id
		  WHERE p.published = ?
		  GROUP BY p.id ORDER BY comments DESC, p.title`, true)
	if err != nil {
		return err
	}
	for _, s := range stats {
		fmt.Printf("  %2d  %s\n", s.Comments, s.Title)
	}

	// A reader leaves a comment — written in a transaction, then read back.
	section("A reader comments (in a transaction)")
	if err := addComment(ctx, db, post.ID, "dave", "Bookmarked — thanks!"); err != nil {
		return err
	}
	count, err := query.Select[Comment](db).Where("post_id = ?", post.ID).Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  added a comment; %q now has %d comments\n", post.Title, count)
	return nil
}

func migrate(ctx context.Context, db *liteorm.DB) error {
	section("AutoMigrate")
	if err := orm.AutoMigrateAll(ctx, db, Author{}, Tag{}, Post{}, Comment{}); err != nil {
		return err
	}
	fmt.Println("created authors, tags, posts, comments (+ post_tags junction)")
	return nil
}

func seed(ctx context.Context, db *liteorm.DB) error {
	authors := orm.NewRepo[Author](db)
	tags := orm.NewRepo[Tag](db)
	posts := orm.NewRepo[Post](db)
	comments := orm.NewRepo[Comment](db)

	ada := Author{Name: "Ada Lovelace", Email: "ada@example.com", Bio: "first programmer"}
	grace := Author{Name: "Grace Hopper", Email: "grace@example.com", Bio: "compiler pioneer"}
	for _, a := range []*Author{&ada, &grace} {
		if err := authors.Create(ctx, a); err != nil {
			return err
		}
	}

	golang := Tag{Name: "golang"}
	goTag := Tag{Name: "go"}
	theory := Tag{Name: "theory"}
	for _, t := range []*Tag{&golang, &goTag, &theory} {
		if err := tags.Create(ctx, t); err != nil {
			return err
		}
	}

	drafts := []struct {
		post Post
		tags []*Tag
	}{
		{Post{AuthorID: ada.ID, Title: "Generics in Go", Body: "Type parameters, finally.", Published: true}, []*Tag{&golang, &goTag}},
		{Post{AuthorID: ada.ID, Title: "Iterators in Go 1.23", Body: "range-over-func explained.", Published: true}, []*Tag{&golang}},
		{Post{AuthorID: grace.ID, Title: "On Compilers", Body: "A short history.", Published: true}, []*Tag{&theory}},
		{Post{AuthorID: ada.ID, Title: "Untitled draft", Body: "WIP.", Published: false}, nil}, // hidden from the homepage
	}
	for i := range drafts {
		if err := posts.Create(ctx, &drafts[i].post); err != nil { // BeforeCreate sets the slug
			return err
		}
		if len(drafts[i].tags) == 0 {
			continue
		}
		rel, err := orm.Assoc[Post, Tag](db, "Tags", &drafts[i].post)
		if err != nil {
			return err
		}
		if err := rel.Append(ctx, drafts[i].tags...); err != nil {
			return err
		}
	}

	generics, compilers := drafts[0].post, drafts[2].post
	for _, c := range []*Comment{
		{PostID: generics.ID, Author: "bob", Body: "Great write-up!"},
		{PostID: generics.ID, Author: "cleo", Body: "The type-inference section helped."},
		{PostID: compilers.ID, Author: "erin", Body: "Classic topic."},
	} {
		if err := comments.Create(ctx, c); err != nil {
			return err
		}
	}
	fmt.Printf("seeded %d authors, %d tags, %d posts (1 draft), %d comments\n", 2, 3, len(drafts), 3)
	return nil
}

// addComment writes a comment inside a transaction — the unit of work for a reader
// action — rolling back on any error.
func addComment(ctx context.Context, db *liteorm.DB, postID int64, author, body string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	if err := orm.NewRepo[Comment](tx).Create(ctx, &Comment{PostID: postID, Author: author, Body: body}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func section(s string) { fmt.Printf("\n── %s ──\n", s) }

// countOf renders "1 comment" / "2 comments".
func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func slugify(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

func authorName(a *Author) string {
	if a == nil {
		return "(unknown)"
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

func hashtags(ts []Tag) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('#')
		b.WriteString(t.Name)
	}
	return b.String()
}
