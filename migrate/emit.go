package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WritePair writes a golang-migrate-style migration pair into dir:
//
//	<version>_<slug>.up.sql
//	<version>_<slug>.down.sql
//
// version is zero-padded to at least six digits; slug is name lowercased with
// runs of non-alphanumeric characters collapsed to single underscores. The files
// are exactly the split format [Load] reads back, so a migration generated from
// a model diff (orm.GenerateMigration) drops straight into the runner an adopter
// already uses. dir is created if missing.
//
// An empty up is an error. An empty down is written as a single comment so the
// step is explicitly irreversible rather than silently empty — [Migrator.Down]
// then reports it as irreversible instead of treating "no statements" as success.
func WritePair(dir string, version uint64, name, up, down string) (upPath, downPath string, err error) {
	if strings.TrimSpace(up) == "" {
		return "", "", fmt.Errorf("migrate: WritePair: empty up migration for %q", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	prefix := fmt.Sprintf("%06d_%s", version, slugify(name))
	upPath = filepath.Join(dir, prefix+".up.sql")
	downPath = filepath.Join(dir, prefix+".down.sql")
	if strings.TrimSpace(down) == "" {
		down = "-- (irreversible: no down migration)"
	}
	if err := os.WriteFile(upPath, []byte(withTrailingNewline(up)), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(downPath, []byte(withTrailingNewline(down)), 0o644); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}

// slugify lowercases s and collapses non-alphanumeric runs to single
// underscores, so the result is a valid, readable migration-name segment. An
// empty result falls back to "migration".
func slugify(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
		} else if !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "migration"
	}
	return out
}

func withTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
