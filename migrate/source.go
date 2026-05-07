package migrate

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	splitRe  = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`) // golang-migrate
	singleRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)            // goose-annotated or plain
)

// Load reads migrations from the root of fsys (e.g. an embed.FS subtree). It
// auto-detects three on-disk formats so adopters keep their history:
//   - golang-migrate split: NNN_name.up.sql / NNN_name.down.sql
//   - goose-annotated single file: -- +goose Up / -- +goose Down
//   - plain numbered single file: NNN_name.sql (up-only)
//
// Returned migrations are sorted by version.
func Load(fsys fs.FS) ([]Migration, error) {
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	byV := map[uint64]*Migration{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		content, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		ver, name, dir, ok := parseName(e.Name())
		if !ok {
			return nil, fmt.Errorf("migrate: unrecognized migration filename %q (want NNN_name.sql or NNN_name.up/down.sql)", e.Name())
		}
		mig := byV[ver]
		if mig == nil {
			mig = &Migration{Version: ver, Name: name}
			byV[ver] = mig
		}
		switch dir {
		case "up":
			mig.Up = string(content)
		case "down":
			mig.Down = string(content)
		default: // single file
			mig.Up, mig.Down = splitGoose(string(content))
		}
	}
	out := make([]Migration, 0, len(byV))
	for _, m := range byV {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b Migration) int {
		return int(a.Version) - int(b.Version)
	})
	return out, nil
}

func parseName(file string) (version uint64, name, dir string, ok bool) {
	if mt := splitRe.FindStringSubmatch(file); mt != nil {
		v, err := strconv.ParseUint(mt[1], 10, 64)
		if err != nil {
			return 0, "", "", false
		}
		return v, mt[2], mt[3], true
	}
	if mt := singleRe.FindStringSubmatch(file); mt != nil {
		v, err := strconv.ParseUint(mt[1], 10, 64)
		if err != nil {
			return 0, "", "", false
		}
		return v, mt[2], "", true
	}
	return 0, "", "", false
}

// splitGoose extracts up/down sections from a goose- or sql-migrate-annotated
// file (-- +goose Up / -- +goose Down, also -- +migrate). A file with no markers
// is treated as up-only.
func splitGoose(content string) (up, down string) {
	var ups, downs []string
	section := ""
	seen := false
	for ln := range strings.SplitSeq(content, "\n") {
		t := strings.ToLower(strings.TrimSpace(ln))
		switch {
		case strings.HasPrefix(t, "-- +goose up"), strings.HasPrefix(t, "-- +migrate up"):
			section, seen = "up", true
			continue
		case strings.HasPrefix(t, "-- +goose down"), strings.HasPrefix(t, "-- +migrate down"):
			section, seen = "down", true
			continue
		case strings.HasPrefix(t, "-- +goose statement"):
			continue // statement-boundary hint; our splitter handles statements
		}
		switch section {
		case "up":
			ups = append(ups, ln)
		case "down":
			downs = append(downs, ln)
		}
	}
	if !seen {
		return content, ""
	}
	return strings.Join(ups, "\n"), strings.Join(downs, "\n")
}
