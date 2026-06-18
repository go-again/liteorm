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
    for m in {{ mods }}; do (cd "$m" && go build ./...); done
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
# Prepare a multi-module release. Bumps every internal cross-module require
# (liteorm.org*) to VERSION across the publishable modules — and gosqlite.org to
# GOSQLITE when given — verifies the workspace still builds, then PRINTS the exact
# ordered tag/push plan. It edits go.mod only: it never commits, tags, or pushes
# (run the printed git commands yourself). Module set and dependency edges are
# discovered from go.work + each go.mod, so this adapts as modules come and go.
#
# Local `replace` directives are dev-only and ignored by consumers, so they stay.
#
#   just release v0.1.0           # bump liteorm.org* requires to v0.1.0
#   just release v0.1.0 v0.3.0    # also pin gosqlite.org@v0.3.0 (required before
#                                 # dialect/sqlite can be published functionally)
release VERSION GOSQLITE="":
    #!/usr/bin/env bash
    set -euo pipefail
    v='{{ VERSION }}'; gq='{{ GOSQLITE }}'
    semver='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
    [[ "$v"  =~ $semver ]] || { echo "✗ VERSION must look like v1.2.3 or v1.2.3-rc.1 (got '$v')"  >&2; exit 1; }
    [[ -z "$gq" || "$gq" =~ $semver ]] || { echo "✗ GOSQLITE must look like v1.2.3 (got '$gq')" >&2; exit 1; }

    # Publishable = every workspace module except the examples and the test-only
    # conformance module (those are never tagged or imported by anyone).
    publishable() { case "$1" in ./examples/*|./conformance) return 1 ;; *) return 0 ;; esac; }

    echo "→ bumping internal requires to $v${gq:+, gosqlite.org to $gq}"
    bumped=0
    for m in {{ mods }}; do
        publishable "$m" || continue
        [ -f "$m/go.mod" ] || continue
        # Each REQUIRED liteorm.org* path in this go.mod. Require lines read
        # "<path> v<ver>"; replace lines use "=>" and the module line carries no
        # version, so neither matches. The space before 'v' stops liteorm.org from
        # also catching liteorm.org/gen.
        for p in $(grep -oE "liteorm\.org(/[A-Za-z0-9._/-]+)? v[0-9][^[:space:]]*" "$m/go.mod" | sed -E 's/ v.*//' | sort -u); do
            (cd "$m" && go mod edit -require="$p@$v"); echo "    $m: $p → $v"; bumped=1
        done
        if [ -n "$gq" ] && grep -qE "(^|[[:space:]])gosqlite\.org v[0-9]" "$m/go.mod"; then
            (cd "$m" && go mod edit -require="gosqlite.org@$gq"); echo "    $m: gosqlite.org → $gq"; bumped=1
        fi
    done
    [ "$bumped" -eq 1 ] || echo "    (no internal requires found to bump)"

    echo "→ verifying the workspace still builds (go.work resolves modules locally)"
    just build

    echo
    echo "→ go.mod changes:"
    git diff --stat -- '*go.mod' 2>/dev/null || echo "    (not a git repo — review go.mod diffs by hand)"

    echo
    echo "════════ RELEASE PLAN — run these yourself; nothing below was executed ════════"
    echo "  git add -A && git commit -m 'release $v'"
    echo "  git tag $v                              # root module: liteorm.org"
    for m in {{ mods }}; do
        publishable "$m" || continue
        case "$m" in .|./) continue ;; esac         # root tagged above
        echo "  git tag ${m#./}/$v"
    done
    echo "  git push origin HEAD --tags             # commit + every tag, together"
    echo
    echo "  Before consumers can 'go get' these, ensure:"
    echo "    • the liteorm.org go-import vanity meta is served (module path → GitHub repo)"
    if [ -n "$gq" ]; then
        echo "    • gosqlite.org@$gq is already a published tag"
    else
        echo "    • dialect/sqlite still requires its current gosqlite.org version — re-run with a"
        echo "      published gosqlite version as the 2nd arg before publishing dialect/sqlite"
    fi
