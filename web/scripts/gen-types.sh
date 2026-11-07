#!/bin/sh
# Writes web/src/lib/gen/ from the agent's Go structs (scripts/tygo.yaml).
# tygo is pinned here and run with `go run`, so nothing is installed. Run it
# after changing a struct the console reads, and commit what it writes.
#
#   npm run gen:types             write src/lib/gen/
#   npm run gen:types -- --check  write it, and fail if that is not what git has
set -eu

web="$(cd "$(dirname "$0")/.." && pwd)"
cd "$web/../agent"
go run github.com/gzuidhof/tygo@v0.2.21 generate --config "$web/scripts/tygo.yaml"

if [ "${1:-}" = "--check" ]; then
  changed="$(git -C "$web" status --porcelain -- src/lib/gen)"
  if [ -n "$changed" ]; then
    echo "src/lib/gen/ is behind the agent's structs - commit what gen:types wrote:" >&2
    echo "$changed" >&2
    exit 1
  fi
fi
