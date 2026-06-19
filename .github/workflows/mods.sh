#!/usr/bin/env bash
# Print the workspace's IN-REPO module directories (relative, forward-slash), one
# per line — the same set the justfile sweeps. Parsed from go.work so CI and local
# never drift. Forward-slash relative paths keep `cd "$m"` portable under Git Bash
# on Windows (unlike `go list -m -f {{.Dir}}`, which is OS-native).
#
# Only `.`/`./…` entries are emitted: a developer building against a sibling
# checkout may add an out-of-repo overlay (e.g. `use ../../sqlite`) to their local
# go.work, and that path is absent from the CI checkout — so it is skipped here to
# keep the gated module set in-repo. `just work-check` rejects such an entry in the
# committed go.work outright.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
awk '
  /^use[ \t]*\(/ { inblk = 1; next }
  inblk && /^\)/ { inblk = 0; next }
  inblk          { gsub(/[ \t]/, ""); if ($0 ~ /^\./ && $0 !~ /^\.\./) print; next }
  /^use[ \t]/    { if ($2 ~ /^\./ && $2 !~ /^\.\./) print $2 }
' "$root/go.work"
