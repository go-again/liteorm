package liteorm

import (
	"fmt"
	"testing"
)

func TestErrorClassifiers(t *testing.T) {
	// Each helper matches its sentinel even when wrapped (the normalized error
	// dual-wraps sentinel + driver error), and rejects the others.
	cases := []struct {
		name string
		is   func(error) bool
		match,
		other error
	}{
		{"unique", IsUniqueViolation, ErrUniqueViolation, ErrForeignKey},
		{"fk", IsForeignKeyViolation, ErrForeignKey, ErrUniqueViolation},
		{"notnull", IsNotNullViolation, ErrNotNull, ErrCheck},
		{"check", IsCheckViolation, ErrCheck, ErrNotNull},
		{"notfound", IsNotFound, ErrNoRows, ErrUniqueViolation},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("create: %w: driver said no", c.match)
		if !c.is(c.match) || !c.is(wrapped) {
			t.Errorf("%s: helper should match its sentinel (direct and wrapped)", c.name)
		}
		if c.is(c.other) {
			t.Errorf("%s: helper matched the wrong sentinel", c.name)
		}
		if c.is(nil) {
			t.Errorf("%s: helper matched nil", c.name)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(ErrDeadlock) || !IsRetryable(fmt.Errorf("tx: %w", ErrSerialization)) {
		t.Error("IsRetryable should match deadlock and serialization (wrapped too)")
	}
	if IsRetryable(ErrUniqueViolation) || IsRetryable(nil) {
		t.Error("IsRetryable should not match a non-transient error or nil")
	}
}
