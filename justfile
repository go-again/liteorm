# liteorm — common operations (multi-module workspace).
#
# Install just from https://just.systems. Run `just` (no args) for the default
# recipe (build + test + lint). Recipes that span the whole workspace iterate
# every module listed in go.work, since `go ./...` only covers one module.
# Workspace module dirs, space-separated, parsed from go.work (". ./conformance ...").

set dotenv-load := true

mods := `awk '/^use \(/{f=1;next} /^\)/{f=0;next} f{gsub(/[ \t]/,"");printf "%s ",$0}' go.work`

# Default recipe: a fast pre-commit gate.
default: build test lint

# List every recipe.
help:
    @just --list

# Build every package in every workspace module (catches interface drift).
build:
    #!/usr/bin/env bash
    set -euo pipefail
    # Send any executables (the example main packages) to a throwaway dir so no
    # binaries are left in the tree. Modules with no main package error on "-o
    # <dir>/" and fall back to an in-place compile check; a real compile error
    # still surfaces through that fallback.
    out="$(mktemp -d)"; trap 'rm -rf "$out"' EXIT
    for m in {{ mods }}; do
        (cd "$m" && { go build -o "$out/" ./... 2>/dev/null || go build ./...; })
    done
    echo "build ok"

# Run the test suite across every module.
test:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go test -count=1 -timeout 2m ./...); done

# Verbose test run for diagnosing a flake.
test-v:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go test -count=1 -timeout 5m -v ./...); done

# Run a single named test (or regex) wherever it lives. Usage: just test-one TestMSSQL
test-one PATTERN:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go test -count=1 -run "{{ PATTERN }}" -v ./... 2>/dev/null) || true; done

# Run Go benchmarks (allocs + ns/op) for the hot-path packages. Pass a regex via

# PKG to narrow, e.g. `just bench PKG=./query`.
bench PKG="./...":
    @go test -run '^$' -bench . -benchmem {{ PKG }}

# checkptr is disabled below: -race enables it, and modernc.org/libc (under
# gosqlite's SQLite engine) does intentional pointer arithmetic over its simulated
# heap that checkptr false-flags on every DB open. Data-race detection stays on.
# Race-detector pass across every module.
test-race:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go test -race -gcflags=all=-d=checkptr=0 -count=1 -timeout 5m ./...); done

# fmt-check runs first — cheapest, and the most common local-only CI failure.

# Lint = format check + vet + golangci-lint + gopls modernize (CI parity).
lint: fmt-check vet golangci modernize

# go vet across every module.
vet:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go vet ./...); done
    echo "vet ok"

# Install: `brew install golangci-lint` or see https://golangci-lint.run.

# Run golangci-lint (config .golangci.yml; bundles staticcheck) in every module.
golangci:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v golangci-lint >/dev/null; then
        echo "golangci-lint not installed; see https://golangci-lint.run"; exit 1
    fi
    cfg="$(pwd)/.golangci.yml"
    for m in {{ mods }}; do (cd "$m" && golangci-lint run --config "$cfg" ./...); done
    echo "golangci ok"

# Run via `go run` so no separate install is needed; `^go:` strips the
# toolchain's auto-download breadcrumbs so they don't trip the empty-output gate.

# gopls modernize: flag Go-version-bump idioms (range-over-int, TypeFor, ...).
modernize:
    #!/usr/bin/env bash
    set -euo pipefail
    tool=golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest
    fail=0
    for m in {{ mods }}; do
        # Skip generated files (.pb.go/.gen.go) — like golangci, generated code is exempt from style modernization.
        out=$(cd "$m" && go run "$tool" ./... 2>&1 | grep -v '^go: ' | grep -v '^exit status' | grep -vE '\.(pb|gen)\.go:' || true)
        if [ -n "$out" ]; then echo "## $m"; echo "$out"; fail=1; fi
    done
    [ "$fail" -eq 0 ] && echo "modernize ok"

# gofmt diff (read-only). Fails if any non-dotfile .go file would be reformatted.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(gofmt -d $(find . -name '*.go' -not -path './.*/*'))
    if [ -n "$out" ]; then echo "$out"; exit 1; fi
    echo "fmt ok"

# Apply gofmt in place.
fmt:
    @gofmt -w $(find . -name '*.go' -not -path './.*/*')

# go mod tidy in every module, then resync the workspace.
tidy:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go mod tidy); done
    go work sync

# Resync the go.work build list to the modules' go.mod files.
work-sync:
    go work sync

# Bump every intra-repo liteorm.org* require to VERSION, then resync (run before
# tagging a release). e.g. `just pin v0.9.0`. gosqlite.org is pinned separately.
pin VERSION:
    python3 dev/pin.py {{ VERSION }}
    go work sync

# Run one example by name (e.g. `just example blog`).
example NAME:
    @cd examples/{{ NAME }} && go run .

# List every runnable example.
examples-list:
    @find examples -name main.go | sed 's|/main.go||; s|^examples/||' | sort

# Smoke-test every example (each runs in its own module and prints to stdout).
examples:
    #!/usr/bin/env bash
    set -euo pipefail
    repo="$(pwd)"
    for ex in $(find examples -name main.go | sed 's|/main.go||' | sort); do
        echo "=== $ex ==="
        (cd "$repo/$ex" && go run .) || { echo "FAILED: $ex"; exit 1; }
    done
    echo "examples ok"

# --- Live databases for the cross-dialect conformance suite ----------------
# These start throwaway containers via Docker (any engine — Docker Desktop,
# Colima, Podman with a docker shim, a remote DOCKER_HOST, etc.). A running
# Docker daemon is required.
# Host ports — high numbers to avoid colliding with local/standard DB ports.
# Override per-invocation via env, e.g. `LITEORM_PG_PORT=15500 just db-up test-live`.

pg_port := env("LITEORM_PG_PORT", "15432")
mysql_port := env("LITEORM_MYSQL_PORT", "13306")
mssql_port := env("LITEORM_MSSQL_PORT", "11433")

# DSNs derive from the ports above.

pg_dsn := "postgres://postgres:pw@localhost:" + pg_port + "/liteorm?sslmode=disable"
mysql_dsn := "root:pw@tcp(localhost:" + mysql_port + ")/liteorm?parseTime=true"
mssql_dsn := "sqlserver://sa:Password123!@localhost:" + mssql_port + "?database=master&encrypt=disable"

# Start Postgres + MySQL + MSSQL (Azure SQL Edge, ARM-native) and wait for ready.
db-up:
    #!/usr/bin/env bash
    set -euo pipefail
    docker info >/dev/null 2>&1 || { echo "Docker daemon not reachable — start Docker first."; exit 1; }
    docker rm -f liteorm-pg liteorm-mysql liteorm-mssql >/dev/null 2>&1 || true
    docker run -d --name liteorm-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=liteorm -p {{ pg_port }}:5432 postgres:16 >/dev/null
    docker run -d --name liteorm-mysql -e MYSQL_ROOT_PASSWORD=pw -e MYSQL_DATABASE=liteorm -p {{ mysql_port }}:3306 mysql:8 >/dev/null
    docker run -d --name liteorm-mssql -e 'ACCEPT_EULA=1' -e 'MSSQL_SA_PASSWORD=Password123!' -p {{ mssql_port }}:1433 mcr.microsoft.com/azure-sql-edge:latest >/dev/null
    echo "waiting for databases..."
    # Postgres/MySQL: host-side TCP connect on the mapped port. Robust against
    # MySQL's two-phase init (its setup server runs --skip-networking, so the
    # mapped TCP port only opens once the real server accepts connections).
    wait_tcp() {
        for _ in $(seq 1 240); do
            if (exec 3<>/dev/tcp/127.0.0.1/"$2") 2>/dev/null; then echo "  $1 ready on :$2"; return 0; fi
            sleep 0.5
        done
        echo "  $1 TIMEOUT on :$2"; return 1
    }
    # MSSQL accepts TCP before the engine is login-ready, so wait on its log line
    # ("SQL Server is now ready for client connections"); also fail fast on a crash.
    wait_mssql() {
        for _ in $(seq 1 240); do
            if docker logs liteorm-mssql 2>&1 | grep -qi "SQL Server is now ready for client connections"; then echo "  mssql ready on :{{ mssql_port }}"; return 0; fi
            if ! docker ps --filter name=liteorm-mssql --filter status=running -q | grep -q .; then
                echo "  mssql container exited:"; docker logs liteorm-mssql 2>&1 | tail -4; return 1
            fi
            sleep 1
        done
        echo "  mssql TIMEOUT"; return 1
    }
    wait_tcp postgres {{ pg_port }}
    wait_tcp mysql {{ mysql_port }}
    wait_mssql

# SQLite runs always; PG/MySQL/MSSQL via the DSNs. Run `just db-up` first.

# Run the conformance suite live against the running databases.
test-live:
    cd conformance && \
    LITEORM_PG_DSN="{{ pg_dsn }}" \
    LITEORM_MYSQL_DSN="{{ mysql_dsn }}" \
    LITEORM_MSSQL_DSN="{{ mssql_dsn }}" \
    go test -count=1 -timeout 5m ./...

# Same, with the race detector (checkptr disabled — see test-race).
test-live-race:
    cd conformance && \
    LITEORM_PG_DSN="{{ pg_dsn }}" \
    LITEORM_MYSQL_DSN="{{ mysql_dsn }}" \
    LITEORM_MSSQL_DSN="{{ mssql_dsn }}" \
    go test -race -gcflags=all=-d=checkptr=0 -count=1 -timeout 10m ./...

# Run the orm suite (conformance/ormsuite) against each backend in turn — SQLite,

# then the live servers. The suite picks its dialect from LITEORM_DIALECT.
test-ormsuite:
    cd conformance && go test -count=1 ./ormsuite/
    cd conformance && LITEORM_DIALECT=postgres LITEORM_PG_DSN="{{ pg_dsn }}" go test -count=1 ./ormsuite/
    cd conformance && LITEORM_DIALECT=mysql LITEORM_MYSQL_DSN="{{ mysql_dsn }}" go test -count=1 ./ormsuite/
    cd conformance && LITEORM_DIALECT=mssql LITEORM_MSSQL_DSN="{{ mssql_dsn }}" go test -count=1 ./ormsuite/

# Remove the database containers (the Docker daemon is left running).
db-down:
    @docker rm -f liteorm-pg liteorm-mysql liteorm-mssql >/dev/null 2>&1 || true
    @echo "db containers removed"

# Full CI parity: everything, in order.
ci: build test test-race lint examples

# Clean test artifacts.
clean:
    @find . -name '*.test' -not -path './.*' -delete 2>/dev/null || true

# --- Release ---------------------------------------------------------------
# Prepare a multi-module release. By default this is SELECTIVE: it tags only the
# modules whose tracked files changed since their last tag — most releases touch a
# subset, so unchanged modules keep their existing tag instead of accruing a fresh
# one every release. Pass `all` as the 3rd arg for a LOCKSTEP release that bumps
# and tags every publishable module (use it for a breaking root change, where
# dependents must be re-released even if their own code didn't move).
#
# For each released module it bumps the internal liteorm.org* requires to the right
# version — the new VERSION for deps also being released, otherwise that dep's
# current latest tag — pins gosqlite.org to GOSQLITE when given, verifies the
# workspace builds, then PRINTS the ordered tag/push plan. It edits go.mod only: it
# never commits, tags, or pushes (run the printed git commands yourself). Local
# `replace` directives are dev-only and stay; the module set and dependency edges
# are discovered from go.work + each go.mod.
#
#   just release v0.10.0           # selective: tag only changed modules
#   just release v0.10.0 v0.9.0    # also pin gosqlite.org@v0.9.0 in released modules
#   just release v0.10.0 '' all    # lockstep: bump + tag every publishable module
release VERSION GOSQLITE="" ALL="":
    #!/usr/bin/env bash
    set -euo pipefail
    v='{{ VERSION }}'; gq='{{ GOSQLITE }}'; all='{{ ALL }}'
    semver='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
    [[ "$v"  =~ $semver ]] || { echo "✗ VERSION must look like v1.2.3 or v1.2.3-rc.1 (got '$v')"  >&2; exit 1; }
    [[ -z "$gq" || "$gq" =~ $semver ]] || { echo "✗ GOSQLITE must look like v1.2.3 (got '$gq')" >&2; exit 1; }
    [[ -z "$all" || "$all" == "all" ]] || { echo "✗ 3rd arg must be empty or 'all' (got '$all')" >&2; exit 1; }
    mods_list="{{ mods }}"

    # Publishable = every workspace module except the examples and the test-only
    # conformance module (those are never tagged or imported by anyone).
    publishable() { case "$1" in ./examples/*|./conformance) return 1 ;; *) return 0 ;; esac; }
    # tagpfx DIR -> tag prefix ("" for root, "dialect/sqlite/" for a submodule).
    tagpfx() { case "$1" in .|./) echo "" ;; *) echo "${1#./}/" ;; esac; }
    # lastver DIR -> highest existing version tag for that module, or "" if none.
    lastver() {
        local pfx; pfx=$(tagpfx "$1")
        if [ -z "$pfx" ]; then git tag -l 'v*' | sort -V | tail -1
        else git tag -l "${pfx}v*" | sed "s#^${pfx}##" | sort -V | tail -1; fi
    }
    # changed DIR -> exit 0 if the module's tracked files differ from its last tag.
    # A never-tagged module counts as changed; root excludes every nested module.
    changed() {
        local last; last=$(lastver "$1")
        [ -z "$last" ] && return 0
        local pfx; pfx=$(tagpfx "$1")
        if [ -z "$pfx" ]; then
            local o; local paths=(.)
            for o in $mods_list; do case "$o" in .|./) ;; *) paths+=( ":(exclude)${o#./}" ) ;; esac; done
            ! git diff --quiet "$last" HEAD -- "${paths[@]}"
        else
            ! git diff --quiet "${pfx}${last}" HEAD -- "${1#./}"
        fi
    }

    # Decide the release set (changed modules, or all publishable under `all`).
    rel=(); skip=()
    for m in $mods_list; do
        publishable "$m" || continue
        [ -f "$m/go.mod" ] || continue
        if [ -n "$all" ] || changed "$m"; then rel+=("$m"); else skip+=("$m"); fi
    done
    if [ ${#rel[@]} -eq 0 ]; then
        echo "→ no publishable module changed since its last tag — nothing to release."
        exit 0
    fi

    in_rel() { local x; for x in "${rel[@]}"; do [ "$x" = "$1" ] && return 0; done; return 1; }
    # targetver DIR -> VERSION when DIR is part of this release, else its current tag.
    targetver() { if in_rel "$1"; then echo "$v"; else lastver "$1"; fi; }
    # path_dir liteorm.org[/sub] -> the module directory it refers to.
    path_dir() { case "$1" in liteorm.org) echo "." ;; liteorm.org/*) echo "./${1#liteorm.org/}" ;; esac; }
    label() { local m; for m in "$@"; do [ "$m" = . ] && printf 'liteorm.org ' || printf '%s ' "${m#./}"; done; }

    echo "→ releasing $v: $(label "${rel[@]}")"
    [ ${#skip[@]} -gt 0 ] && echo "  kept at current tag: $(label "${skip[@]}")"
    [ -n "$all" ] && echo "  (all: lockstep over every publishable module)"

    echo "→ bumping internal requires${gq:+, gosqlite.org to $gq}"
    for m in "${rel[@]}"; do
        for p in $(grep -oE "liteorm\.org(/[A-Za-z0-9._/-]+)? v[0-9][^[:space:]]*" "$m/go.mod" | sed -E 's/ v.*//' | sort -u); do
            dep=$(path_dir "$p"); tv=$(targetver "$dep")
            [ -n "$tv" ] || { echo "✗ $m requires $p, which has no released version to point at — release it too, or use 'all'" >&2; exit 1; }
            (cd "$m" && go mod edit -require="$p@$tv"); echo "    $m: $p → $tv"
        done
        if [ -n "$gq" ] && grep -qE "(^|[[:space:]])gosqlite\.org v[0-9]" "$m/go.mod"; then
            # gosqlite is an external dep (no local replace), so use `go get`, not
            # `go mod edit` — it updates go.sum with the new version's hash too.
            (cd "$m" && go get "gosqlite.org@$gq"); echo "    $m: gosqlite.org → $gq"
        fi
    done

    echo "→ verifying the workspace still builds (go.work resolves modules locally)"
    just build

    echo
    echo "→ go.mod / go.sum changes:"
    git diff --stat -- '*go.mod' '*go.sum' 2>/dev/null || echo "    (not a git repo — review the diffs by hand)"

    # The plan below is emitted as bare commands (no inline comments) so the whole
    # block copy-pastes cleanly — zsh, unlike bash, does not treat '#' as a comment
    # interactively. Context goes here, above the block; caveats go below it.
    echo
    echo "→ commits with 'git add -u' (only the tracked go.mod/go.sum bumps — never build artifacts),"
    echo "  tags the root as '$v' and each changed submodule as '<path>/$v'."
    [ ${#skip[@]} -gt 0 ] && echo "  Unchanged, kept at their current tag: $(label "${skip[@]}")"
    echo
    echo "──────── RELEASE PLAN (copy/paste everything between the lines) ────────"
    echo "git add -u && git commit -m 'release $v'"
    for m in "${rel[@]}"; do
        if [ "$m" = "." ] || [ "$m" = "./" ]; then echo "git tag $v"
        else echo "git tag ${m#./}/$v"; fi
    done
    echo "git push origin HEAD --tags"
    echo "───────────────────────────────────────────────────────────────────────"
    echo
    echo "Before consumers can 'go get' these, ensure:"
    echo "  • the liteorm.org go-import vanity meta is served (module path → GitHub repo)"
    [ -n "$gq" ] && echo "  • gosqlite.org@$gq is already a published tag"
