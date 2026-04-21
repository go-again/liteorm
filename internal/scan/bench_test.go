package scan

import (
	"testing"
	"time"
)

// benchRow is a representative ~8-column model: ints, strings, a float, a bool
// (exercises the boolScanner path), and a time.
type benchRow struct {
	ID        int64
	Name      string
	Email     string
	Age       int64
	Active    bool
	Score     float64
	CreatedAt time.Time
	Notes     string
}

func (benchRow) TableName() string { return "bench_rows" }

// fakeRows is a deterministic in-memory cursor over n rows. Its Scan writes a
// plausible value into each destination so the real scan branches (direct field
// address, boolScanner, skip target) are exercised without a database.
type fakeRows struct {
	cols []string
	n    int
	i    int
}

func newFakeRows(n int) *fakeRows {
	return &fakeRows{cols: []string{"id", "name", "email", "age", "active", "score", "created_at", "notes"}, n: n}
}

func (r *fakeRows) Columns() ([]string, error) { return r.cols, nil }
func (r *fakeRows) Close() error               { return nil }
func (r *fakeRows) Err() error                 { return nil }
func (r *fakeRows) Next() bool {
	if r.i >= r.n {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	for _, d := range dest {
		switch p := d.(type) {
		case *int64:
			*p = int64(r.i)
		case *string:
			*p = "x"
		case *float64:
			*p = 1.5
		case *time.Time:
			*p = time.Unix(int64(r.i), 0).UTC()
		case interface{ Scan(any) error }: // boolScanner
			_ = p.Scan(int64(1))
		case *any:
			*p = nil
		}
	}
	return nil
}

func (r *fakeRows) reset() { r.i = 0 }

func BenchmarkScanAll(b *testing.B) {
	rows := newFakeRows(100)
	b.ReportAllocs()
	for b.Loop() {
		rows.reset()
		out, err := All[benchRow](rows)
		if err != nil || len(out) != 100 {
			b.Fatalf("All: len=%d err=%v", len(out), err)
		}
	}
}

func BenchmarkValues(b *testing.B) {
	v := &benchRow{ID: 1, Name: "a", Email: "a@b.c", Age: 30, Active: true, Score: 9.5, Notes: "n"}
	cols := Columns[benchRow](false)
	b.ReportAllocs()
	for b.Loop() {
		_ = Values(v, cols)
	}
}
