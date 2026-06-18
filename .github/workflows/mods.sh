#!/usr/bin/env bash
# Print the workspace's module directories (relative, forward-slash), one per
# line — the same set the justfile sweeps. Parsed from go.work so CI and local
# never drift. Forward-slash relative paths keep `cd "$m"` portable under Git
# Bash on Windows (unlike `go list -m -f {{.Dir}}`, which is OS-native).
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
awk '
  /^use[ \t]*\(/ { inblk = 1; next }
  inblk && /^\)/ { inblk = 0; next }
  inblk          { gsub(/[ \t]/, ""); if ($0 != "" && $0 !~ /^\/\//) print; next }
  /^use[ \t]/    { print $2 }
' "$root/go.work"
