package query

import (
	"testing"

	"liteorm.org/internal/sqlgen"
)

// BenchmarkSelectConstruct isolates Select[T] construction, which rebuilds the
// table-qualified column list (columnsOf) on every call.
func BenchmarkSelectConstruct(b *testing.B) {
	sess := mockSession{d: sqlgen.SQLite}
	b.ReportAllocs()
	for b.Loop() {
		_ = Select[tuser](sess)
	}
}

// BenchmarkSelectBuild isolates the full build path, which rebuilds the column
// set (columnSet) for predicate validation and copies clause slices in resolved().
func BenchmarkSelectBuild(b *testing.B) {
	sess := mockSession{d: sqlgen.SQLite}
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := Select[tuser](sess).
			Where("age > ?", 18).
			Filter(Col[string]("name").Eq("x")).
			OrderBy("id DESC").
			Limit(10).
			buildSQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpdateBuild covers the write-side build (renderPreds → columnSet).
func BenchmarkUpdateBuild(b *testing.B) {
	sess := mockSession{d: sqlgen.SQLite}
	b.ReportAllocs()
	for b.Loop() {
		up, err := Update[tuser](sess).
			Set("name", "x").
			Filter(Col[int64]("id").Eq(1)).
			resolved()
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := up.Build(sqlgen.SQLite); err != nil {
			b.Fatal(err)
		}
	}
}
