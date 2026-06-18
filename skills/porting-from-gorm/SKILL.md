---
name: porting-from-gorm
description: Use when migrating a gorm codebase to liteorm — running models on gorm tags as-is, rewriting them to native orm tags, and adjusting for what differs.
---

# Porting from gorm

liteorm's `orm` front-end reads `gorm:"..."` struct tags natively, so most gorm models run unchanged. Rewriting to native `orm:"..."` tags is optional cleanup that drops the gorm dependency. The behavioral differences (explicit loading, soft-delete scopes) are the part that needs attention.

## Step 1 — it probably already works

Point the orm Repo at your existing gorm-tagged struct:

```go
type User struct {
    ID    uint   `gorm:"primaryKey"`
    Email string `gorm:"column:email;uniqueIndex;not null"`
}
func (User) TableName() string { return "users" }

_ = orm.AutoMigrate[User](ctx, db)
_ = orm.NewRepo[User](db).Create(ctx, &u)
```

A ported model and the original behave identically; gorm tags are translated by the same resolver.

## Step 2 — rewrite to native tags (optional, drops gorm dep)

`gen.PortSource` rewrites `gorm:"..."` → `orm:"..."` on Go source, and `gorm.DeletedAt` → `sql.NullTime` + a `soft_delete` tag.

```go
out, notes, err := gen.PortSource(src /* []byte of the model file */)
// out = rewritten source; notes = dropped/unsupported tag keys + import hints
```

Then `goimports -w` the result: `gorm.io/gorm` becomes unused and `database/sql` is now required. The porter only edits tag literals and the one field type; all other formatting is preserved.

Tag translation (a sample):

| gorm | orm |
| --- | --- |
| `primaryKey` | `pk` |
| `column:x` | `x` (the column name) |
| `uniqueIndex` / `unique` | `unique` |
| `not null` | `notnull` |
| `default:v` / `size:n` / `type:t` / `check:e` | `default:v` / `size:n` / `type:t` / `check:e` |
| `autoCreateTime` / `autoUpdateTime` | `autocreatetime` / `autoupdatetime` |
| `many2many:t` | `m2m:t` |
| `foreignKey:c` / `references:c` | `fk:c` / `references:c` |
| `gorm.Model` embed | reported with suggested explicit fields (ID + timestamps + soft-delete) |
| `gorm.DeletedAt` | `sql.NullTime` + `soft_delete` |

## Step 3 — adjust for what differs

| gorm | liteorm |
| --- | --- |
| Lazy association loading | None. Call `orm.Load[P,C](ctx, sess, parents, "Field")` explicitly — it's N+1-safe (one query per call). |
| `Preload("A").Preload("B")` | Separate `orm.Load` calls; nested = chain one level at a time. |
| Soft delete hidden automatically + `Unscoped()` | Default reads also hide deleted rows, but the opt-outs are explicit: `IncludeDeleted()` / `OnlyDeleted()` / `ForceDelete`. |
| `gorm.DeletedAt` field type | `sql.NullTime` with the `soft_delete` tag. |
| Pluralized table names by default | Opt-in: `orm.UsePluralTableNames(true)` restores gorm-style plurals globally (`orm.RegisterPlural` for irregulars). Otherwise `snake_case(TypeName)` singular, or pin with `TableName()`. |
| Hooks as methods (silent if mis-signed) | Hooks are typed on T — a wrong signature is a compile error (see orm-models skill). |

## Pitfalls

- `gorm.Model`'s embedded `ID uint` becomes `int64` in liteorm conventions — the porter reports the suggested explicit fields rather than guessing the embed.
- After porting, accessing a relation field without `orm.Load` returns zero — there is no lazy fetch to fall back on.
- Don't forget the `database/sql` import for the rewritten `sql.NullTime` field.

## Deeper

- See the orm-models and codegen skills.
- API: https://pkg.go.dev/liteorm.org/orm and https://pkg.go.dev/liteorm.org/gen
