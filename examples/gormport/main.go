// Command gormport demonstrates the gorm→liteorm codegen porter: it rewrites a
// gorm-tagged model's `gorm:"…"` tags into liteorm-native `orm:"…"` tags (and
// the gorm.DeletedAt type into sql.NullTime), prints the result and a report,
// then proves a model using the ported tags works live with the orm front-end.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"liteorm.org/dialect/sqlite"
	"liteorm.org/gen"
	"liteorm.org/orm"
)

// gormModel is a typical gorm model, tags and all.
const gormModel = `package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        uint           ` + "`gorm:\"primaryKey\"`" + `
	Email     string         ` + "`gorm:\"column:email;uniqueIndex;not null\" json:\"email\"`" + `
	Name      string         ` + "`gorm:\"size:120\"`" + `
	IsAdmin   bool           ` + "`gorm:\"column:is_admin;default:false\"`" + `
	CreatedAt time.Time      ` + "`gorm:\"autoCreateTime\"`" + `
	UpdatedAt time.Time      ` + "`gorm:\"autoUpdateTime\"`" + `
	DeletedAt gorm.DeletedAt ` + "`gorm:\"index\"`" + `
}
`

// PortedUser is what the porter produces (orm tags + sql.NullTime), copied here
// so we can run it live below. Note the dropped gorm.io/gorm import.
type PortedUser struct {
	ID        int64
	Email     string       `orm:"email,unique,notnull" json:"email"`
	Name      string       `orm:"name,size:120"`
	IsAdmin   bool         `orm:"is_admin,default:false"`
	CreatedAt time.Time    `orm:"created_at,autocreatetime"`
	UpdatedAt time.Time    `orm:"updated_at,autoupdatetime"`
	DeletedAt sql.NullTime `orm:"deleted_at,soft_delete"`
}

func (PortedUser) TableName() string { return "users" }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// ---- 1. Port the gorm source ----
	out, notes, err := gen.PortSource([]byte(gormModel))
	if err != nil {
		return err
	}
	fmt.Println("── ported model (gorm tags → orm tags) ──")
	fmt.Print(string(out))
	fmt.Println("\n── porter report ──")
	for _, n := range notes {
		fmt.Printf("  %s  %s\n", n.Pos, n.Message)
	}

	// ---- 2. Prove a model with the ported tags works with the orm front-end ----
	fmt.Println("\n── the ported model, live on SQLite ──")
	ctx := context.Background()
	dir, _ := os.MkdirTemp("", "liteorm-gormport-*")
	defer os.RemoveAll(dir)
	db, err := sqlite.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := orm.AutoMigrate[PortedUser](ctx, db); err != nil {
		return err
	}
	repo := orm.NewRepo[PortedUser](db)
	u := PortedUser{Email: "ada@x.io", Name: "Ada", IsAdmin: true}
	if err := repo.Create(ctx, &u); err != nil {
		return err
	}
	got, err := repo.Get(ctx, u.ID)
	if err != nil {
		return err
	}
	fmt.Printf("  AutoMigrate + Create + Get → #%d %s admin=%v created=%v\n",
		got.ID, got.Email, got.IsAdmin, !got.CreatedAt.IsZero())

	// soft delete uses the ported deleted_at column
	_ = repo.Delete(ctx, &got)
	live, _ := repo.Find(ctx)
	all, _ := repo.IncludeDeleted().Find(ctx)
	fmt.Printf("  soft delete via ported deleted_at: live=%d includeDeleted=%d\n", len(live), len(all))
	return nil
}
