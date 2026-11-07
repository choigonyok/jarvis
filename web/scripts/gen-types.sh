#!/bin/sh
# Writes web/src/lib/gen/ from the agent's Go structs (scripts/tygo.yaml).
# tygo is pinned here and run with `go run`, so nothing is installed. Run it
# after changing a struct the console reads, and commit what it writes.
#
#   npm run gen:types             write src/lib/gen/
#   npm run gen:types -- --check  write it to a scratch folder and fail if
#                                 src/lib/gen/ is not the same; the tree is
#                                 left as it is. cf:build runs this first.
set -eu

web="$(cd "$(dirname "$0")/.." && pwd)"
tygo="github.com/gzuidhof/tygo@v0.2.21"

if ! command -v go >/dev/null 2>&1; then
  echo "gen:types: go 가 없습니다. src/lib/gen/ 은 에이전트의 Go 구조체에서 만들어서 Go 가 있어야 합니다(brew install go)." >&2
  exit 1
fi

cd "$web/../agent"

if [ "${1:-}" != "--check" ]; then
  go run "$tygo" generate --config "$web/scripts/tygo.yaml"
  exit 0
fi

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/gen"
sed "s#\"\.\./web/src/lib/gen/#\"$scratch/gen/#" "$web/scripts/tygo.yaml" >"$scratch/tygo.yaml"
# Every file goes to the scratch folder, or the check would write the tree.
if grep 'output_path:' "$scratch/tygo.yaml" | grep -qv "\"$scratch/gen/"; then
  echo "gen:types --check: scripts/tygo.yaml 의 output_path 가 ../web/src/lib/gen/ 밖을 가리킵니다. 이 스크립트도 같이 고치세요." >&2
  exit 1
fi
if ! go run "$tygo" generate --config "$scratch/tygo.yaml"; then
  echo "gen:types --check: tygo 를 돌리지 못했습니다(위의 오류)." >&2
  exit 1
fi
if ! diff -ru "$web/src/lib/gen" "$scratch/gen" >"$scratch/diff"; then
  echo "gen:types --check: src/lib/gen/ 이 에이전트의 Go 구조체와 다릅니다. npm run gen:types 로 다시 쓰고 커밋한 뒤 다시 빌드하세요." >&2
  head -n 40 "$scratch/diff" >&2
  exit 1
fi
