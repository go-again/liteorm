package sqlite

import (
	"context"
	"fmt"
	"strings"

	liteorm "liteorm.org"
)

// DropTriggers issues DROP TRIGGER IF EXISTS for each name, in order. It is
// idempotent across runs — a trigger that doesn't exist is a silent no-op — so it
// suits a migrate/startup path that cleans up legacy trigger names left behind by
// an earlier schema generation (e.g. a renamed FTS5 external-content trigger).
// Each name is quoted as an identifier by the dialect, which is injection-safe by
// construction; an empty name is rejected and the call short-circuits.
//
// It is SQLite-only: trigger DDL is dialect-specific (PostgreSQL/MySQL trigger
// management differs), so a normalized cross-dialect form is intentionally out of
// scope. This is a schema utility, not a recorded migration — for reviewable,
// ledgered up/down changes use liteorm.org/migrate instead.
func DropTriggers(ctx context.Context, sess liteorm.Session, names ...string) error {
	d := sess.Dialect()
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("sqlite: DropTriggers: empty trigger name")
		}
		q := "DROP TRIGGER IF EXISTS " + string(d.QuoteIdent(nil, name))
		if _, err := sess.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("sqlite: DropTriggers: drop %s: %w", name, err)
		}
	}
	return nil
}
