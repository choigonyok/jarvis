#!/bin/sh
# 적용된 마이그레이션을 기록하고, 아직 안 된 것만 순서대로 돌린다.
#
# 파일을 initdb 디렉터리에 넣지 않은 이유: /docker-entrypoint-initdb.d 는
# 데이터 디렉터리가 빈 첫 부팅에만 실행된다. 스키마를 한 번이라도 고칠
# 생각이라면 그건 마이그레이션이 아니라 일회용 시드다.
#
# 도구를 안 쓴 이유: psql 이 이미 이미지에 있고, 필요한 것은 "어디까지 했나"를
# 적어두는 표 하나다. golang-migrate 를 컨테이너로 하나 더 띄우는 것이
# 이 열 몇 줄보다 싸지 않다.
set -eu

: "${PGHOST:=postgres}"
: "${PGPORT:=5432}"
: "${PGUSER:=jarvis}"
: "${PGDATABASE:=jarvis}"
export PGHOST PGPORT PGUSER PGDATABASE

DIR="${MIGRATIONS_DIR:-/db/migrations}"

# 부팅 순서를 compose 의 healthcheck 에 맡기고 있지만, 컨테이너가 healthy 로
# 바뀐 직후 첫 연결이 거절되는 경우가 있어 짧게 기다린다.
tries=0
until psql -qtAX -c 'select 1' >/dev/null 2>&1; do
  tries=$((tries + 1))
  if [ "$tries" -ge 30 ]; then
    echo "postgres 에 연결하지 못했습니다 ($PGHOST:$PGPORT)" >&2
    exit 1
  fi
  sleep 1
done

psql -qX -v ON_ERROR_STOP=1 -c "
  create table if not exists schema_migrations (
    version    text primary key,
    applied_at timestamptz not null default now()
  )"

# commitments 의 트리거가 벽시계를 구간으로 바꿀 때 읽는 시간대. 데이터베이스에
# 박아두는 이유는 연결마다 set 하는 것을 한 곳에서라도 빼먹으면 그 연결로 들어온
# 일정만 9시간 밀린 구간을 갖게 되고, 그건 조회할 때까지 드러나지 않기 때문이다.
psql -qX -v ON_ERROR_STOP=1 \
  -c "alter database \"$PGDATABASE\" set jarvis.timezone = '${TZ:-Asia/Seoul}'"

applied=0
for file in "$DIR"/*.sql; do
  [ -e "$file" ] || continue
  version=$(basename "$file")

  if [ "$(psql -qtAX -c "select 1 from schema_migrations where version = '$version'")" = "1" ]; then
    continue
  fi

  echo "→ $version"
  # -f 와 -c 는 명령줄에 쓴 순서대로 실행되고, --single-transaction 이 둘을
  # 한 트랜잭션에 묶는다. 그래서 스크립트가 중간에 실패하면 기록도 남지
  # 않는다 - 반쯤 적용된 상태로 "완료"라고 적히는 경우가 없다.
  psql -qX -v ON_ERROR_STOP=1 --single-transaction \
    -f "$file" \
    -c "insert into schema_migrations (version) values ('$version')"
  applied=$((applied + 1))
done

if [ "$applied" -eq 0 ]; then
  echo "마이그레이션: 변경 없음"
else
  echo "마이그레이션: $applied 개 적용"
fi
