package orm

import "testing"

// TestTypeNormalizerEquivalences: each pair is the emitted-DDL spelling vs the
// catalog spelling for the same logical type — they must NOT register as a change.
func TestTypeNormalizerEquivalences(t *testing.T) {
	cases := []struct{ dialect, emitted, introspected string }{
		// Postgres: serial introspects as its underlying int; tz/text spellings differ.
		{"postgres", "BIGSERIAL", "bigint"},
		{"postgres", "BIGINT", "bigint"},
		{"postgres", "TIMESTAMPTZ", "timestamp with time zone"},
		{"postgres", "DOUBLE PRECISION", "double precision"},
		{"postgres", "TEXT", "text"},
		{"postgres", "BYTEA", "bytea"},
		// MySQL: the catalog drops the display width.
		{"mysql", "BIGINT", "bigint"},
		{"mysql", "VARCHAR(255)", "varchar"},
		{"mysql", "TINYINT(1)", "tinyint"},
		{"mysql", "DATETIME", "datetime"},
		// MSSQL: (max)/width dropped.
		{"mssql", "NVARCHAR(255)", "nvarchar"},
		{"mssql", "VARBINARY(MAX)", "varbinary"},
		{"mssql", "BIGINT", "bigint"},
		{"mssql", "BIT", "bit"},
		// SQLite: declared types round-trip.
		{"sqlite", "INTEGER", "INTEGER"},
		{"sqlite", "TEXT", "TEXT"},
		{"sqlite", "TIMESTAMP", "TIMESTAMP"},
	}
	for _, c := range cases {
		if typeChanged(c.dialect, c.introspected, c.emitted) {
			t.Errorf("%s: %q vs %q wrongly flagged as a type change (false positive)", c.dialect, c.introspected, c.emitted)
		}
	}
}

// TestTypeNormalizerRealChanges: genuinely different types must register.
func TestTypeNormalizerRealChanges(t *testing.T) {
	cases := []struct{ dialect, from, to string }{
		{"postgres", "bigint", "TEXT"},
		{"postgres", "text", "BIGINT"},
		{"mysql", "bigint", "VARCHAR(255)"},
		{"mssql", "int", "NVARCHAR(255)"},
		{"sqlite", "INTEGER", "TEXT"},
	}
	for _, c := range cases {
		if !typeChanged(c.dialect, c.from, c.to) {
			t.Errorf("%s: %q -> %q should be a type change", c.dialect, c.from, c.to)
		}
	}
}

// TestTypeNormalizerConservative: an unrecognized type on either side yields no
// change (a missed change is safe; a false positive churns migrations).
func TestTypeNormalizerConservative(t *testing.T) {
	if typeChanged("postgres", "some_custom_domain", "TEXT") {
		t.Error("an unknown DB type must be treated as no change")
	}
	if typeChanged("postgres", "bigint", "CITEXT") {
		t.Error("an unknown model type must be treated as no change")
	}
	if typeChanged("oracle", "NUMBER", "VARCHAR2") {
		t.Error("an unknown dialect must be treated as no change")
	}
}
