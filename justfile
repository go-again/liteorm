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

# Race-detector pass across every module.
test-race:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in {{ mods }}; do (cd "$m" && go test -race -count=1 -timeout 5m ./...); done

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

# Same, with the race detector.
test-live-race:
    cd conformance && \
    LITEORM_PG_DSN="{{ pg_dsn }}" \
    LITEORM_MYSQL_DSN="{{ mysql_dsn }}" \
    LITEORM_MSSQL_DSN="{{ mssql_dsn }}" \
    go test -race -count=1 -timeout 10m ./...

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

# Rebuild the studio frontend (the Prisma-based UI driven by liteorm's adapter)
# into studio/web/dist, which is committed and embedded by the Go module. Run
# this after bumping @prisma/studio-core or editing studio/web/src — then commit
# the regenerated studio/web/dist. Requires Node + npm (dev-only; not needed to
# `go get` the studio).

# Rebuild the studio frontend
studio-ui:
    cd studio/web && npm install && npm run typecheck && npm run build
    @echo "studio/web/dist rebuilt — commit it"

studio-demo: studio-ui
    cd studio/cmd/studio-demo && go run .

# Run the demo locked read-only at compile time (the studio_readonly build tag) —
# what a public, unbreakable online demo deploys: browse/filter/read-SQL only.
studio-demo-readonly: studio-ui
    cd studio/cmd/studio-demo && go run -tags studio_readonly .

# Plug the studio into an EXISTING database with no Go models (catalog-only
# introspection: tables, columns, types, primary keys, foreign-key navigation).
# Needs the matching DB running — `just db-up` starts all three; each demo applies
# its schema.sql once, then serves on its own port (MySQL :8100, Postgres :8101,
# SQL Server :8102).
studio-demo-mysql: studio-ui
    cd studio/cmd/studio-demo-mysql && go run .

studio-demo-postgres: studio-ui
    cd studio/cmd/studio-demo-postgres && go run .

studio-demo-mssql: studio-ui
    cd studio/cmd/studio-demo-mssql && go run .

# Full CI parity: everything, in order.
ci: build test test-race lint examples

# Clean test artifacts.
clean:
    @find . -name '*.test' -not -path './.*' -delete 2>/dev/null || true
