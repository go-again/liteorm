package mysql

import (
	"errors"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	liteorm "liteorm.org"
)

func TestNormalizeError(t *testing.T) {
	cases := []struct {
		num  uint16
		want error
	}{
		{1062, liteorm.ErrUniqueViolation},
		{1452, liteorm.ErrForeignKey},
		{1048, liteorm.ErrNotNull},
		{3819, liteorm.ErrCheck},
		{4025, liteorm.ErrCheck},
		{1213, liteorm.ErrDeadlock},
		{1205, liteorm.ErrDeadlock},
	}
	for _, c := range cases {
		got := normalizeError(&mysqldriver.MySQLError{Number: c.num, Message: "x"})
		if !errors.Is(got, c.want) {
			t.Errorf("number %d → %v, want errors.Is %v", c.num, got, c.want)
		}
	}
	if normalizeError(nil) != nil {
		t.Error("nil must stay nil")
	}
	other := errors.New("boom")
	if !errors.Is(normalizeError(other), other) {
		t.Error("a non-driver error must pass through unchanged")
	}
	// an unmapped MySQL number passes through as-is.
	if got := normalizeError(&mysqldriver.MySQLError{Number: 9999}); errors.Is(got, liteorm.ErrUniqueViolation) {
		t.Error("unmapped number must not map to a sentinel")
	}
}
