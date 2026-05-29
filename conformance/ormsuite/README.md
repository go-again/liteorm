# ormsuite

An integration test suite for liteorm's declarative `orm` front-end, organized by topic over one shared set of models — modeled on gorm's `gorm.io/gorm/tests`, then broadened with patterns borrowed from xorm's test suite and bun's examples so it covers more of what real applications do than any single one of them.

It complements the scenario-style `conformance` suite next door: where that proves the *core contracts* hold on every backend, this exercises the **orm surface** the way an application uses it, and every topic runs unchanged on SQLite, Postgres, MySQL, and SQL Server.

## Coverage by topic

| File | What it covers | Borrowed in spirit from |
| --- | --- | --- |
| `create_test.go` | Create, `CreateInBatches`, `Save` (upsert-by-identity), `FirstOrCreate` | gorm create |
| `firstorinit_test.go` | `FirstOrInit` — load-or-prepare without persisting | gorm `FirstOrInit` |
| `query_test.go` | `First` / `Find` / `Exists`, typed predicates, limit/offset | gorm query |
| `findinbatches_test.go` | `FindInBatches` keyset batch scan, abort-on-error, single-PK guard | gorm/xorm batched scan |
| `update_test.go` | `Update` (bumps autoUpdateTime), `Updates` + `Select`/`Omit`, multi-row update builder | gorm update |
| `delete_test.go` | `Delete`, `ForceDelete`, multi-row delete builder | gorm delete |
| `scopes_test.go` | reusable `Scope[T]` filters, soft-delete tri-state (with / include / only deleted) | gorm scopes + soft delete |
| `multitenant_test.go` | tenant isolation via explicit scope + context-stamped writes (no hidden filter) | bun multi-tenant |
| `fixtures_test.go` | declarative, transactional (atomic) graph seeding | bun fixtures |
| `count_test.go` | `Count`, grouped count via `Into` | gorm count |
| `aggregate_test.go` | `Sum`/`Avg`/`Min`/`Max`/`CountCol`, `GROUP BY` + `HAVING`, single-column pluck | xorm `Sum`/`Sums`, gorm group/having/pluck |
| `predicates_test.go` | `In`/`NotIn`, `Or`/`Not`, `Like`, range, `DISTINCT` | xorm builder conditions, gorm `Or`/`Not`/`Distinct` |
| `joins_test.go` | inner & left join across tables with cross-table projection | gorm `Joins`, xorm `Join` |
| `pagination_test.go` | keyset ("cursor") pagination and classic limit/offset | bun cursor-pagination |
| `iterate_test.go` | lazy row streaming via `iter.Seq2` (range-over-func), early break | xorm `Iterate`/`Rows`, gorm `FindInBatches` |
| `expressions_test.go` | atomic `SetExpr` updates (incr/decr, computed bulk update) | gorm `gorm.Expr`, xorm `Incr` |
| `customtypes_test.go` | custom scalar types (`driver.Valuer`/`sql.Scanner`): CSV and JSON columns | bun custom-type / JSON, xorm conversion |
| `hooks_test.go` | `Before*`/`After*` lifecycle hooks | gorm hooks |
| `transaction_test.go` | commit / rollback, nested savepoints, orm↔query interop on one tx | gorm transactions |
| `txcompose_test.go` | a repository function over `liteorm.Session` composes onto a `*DB` or a `Tx` | bun `IDB` composition |
| `upsert_test.go` | `OnConflict` upsert (query + `orm.Repo.Upsert`) | gorm upsert |
| `tier2_test.go` | `orm.Repo.Upsert`, `GetByKeys` (batch by PK), `Restore` (un-soft-delete) | gorm/xorm ergonomics |
| `rowsaffected_test.go` | keyed `Update`/`Delete` return `ErrNoRows` on no match | hard-errors stance |
| `associations_test.go` | belongs-to, has-one, has-many, many-to-many, self-referential writes + `Assoc` handle | gorm associations |
| `preload_test.go` | N+1-safe eager loading: dotted `LoadPath`, multi-path `Preloader` | gorm preload |
| `filteredload_test.go` | filtered/ordered eager load (`LoadWhere`/`LoadOrderBy`), one query | gorm preload-with-conditions |
| `compositepk_test.go` | composite primary keys (multi-column `Get`/`Update`/`Delete`) | gorm multi-PK, xorm multi-PK |
| `polymorphic_test.go` | polymorphic has-many / has-one (`owner_id` + `owner_type`), cross-owner isolation | gorm / bun polymorphic |

## What it adapts from gorm's tests

The shape is the same — shared models (`User` with `Account`, `Pets`, `Company`, `Manager`, `Team`, `Languages`, `Toys`), a `Config`-driven `GetUser`/`seedUser` builder, `AssertEqual`/`AssertObjEqual`/`CheckUser` helpers, and a `TestMain`-opened `DB`. The models and tests are adapted to liteorm's explicit semantics:

- **Writes never cascade a graph.** `seedUser` persists a user's associations explicitly (create the company, then the user, then the account/pets, then attach languages) — there is no implicit save-the-whole-object-tree.
- **No lazy loading.** A relation is materialized only by an explicit `Load`/`Preloader` call; the preload tests assert each level costs exactly one batched query.
- **Reads drop to the query builder for SQL the Repo doesn't model.** Joins, aggregates, `DISTINCT`, pluck, streaming, and cursor pagination use `query.Select[T]` on the same `DB` — proving the two front-ends share one core and one transaction.

Polymorphic associations and composite primary keys, which a gorm-style suite would normally model with extra types, are first-class here (`polymorphic_test.go`, `compositepk_test.go`).

## Running

By default the suite runs on **CGo-free SQLite** (a throwaway temp database):

```
go test ./conformance/ormsuite/
```

To run it against a server database, select the dialect with `LITEORM_DIALECT` and provide the matching DSN:

```
LITEORM_DIALECT=postgres LITEORM_PG_DSN=... go test ./conformance/ormsuite/
```

`just test-ormsuite` runs it against SQLite and all three live servers in turn (start them with `just db-up` first).
