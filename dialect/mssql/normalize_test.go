package mssql

import (
	"errors"
	"testing"

	mssqldriver "github.com/microsoft/go-mssqldb"
	liteorm "liteorm.org"
)

func TestNormalizeError(t *testing.T) {
	check := func(num int32, msg string, want error) {
		t.Helper()
		got := normalizeError(mssqldriver.Error{Number: num, Message: msg})
		if !errors.Is(got, want) {
			t.Errorf("number %d (%q) → %v, want errors.Is %v", num, msg, got, want)
		}
	}
	check(2627, "", liteorm.ErrUniqueViolation)
	check(2601, "", liteorm.ErrUniqueViolation)
	check(515, "", liteorm.ErrNotNull)
	check(1205, "", liteorm.ErrDeadlock)
	check(547, "The INSERT statement conflicted with the CHECK constraint", liteorm.ErrCheck)
	check(547, "The INSERT statement conflicted with the FOREIGN KEY constraint", liteorm.ErrForeignKey)

	if normalizeError(nil) != nil {
		t.Error("nil must stay nil")
	}
	other := errors.New("boom")
	if !errors.Is(normalizeError(other), other) {
		t.Error("a non-driver error must pass through unchanged")
	}
}
