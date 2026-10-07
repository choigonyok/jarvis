# db

이벤트 로그, 파생 테이블, 회수용 벡터가 전부 한 Postgres 안에 있습니다.
pgvector 는 확장이라 별도 컨테이너가 아닙니다.

```
raw_events (추가 전용)  ← 수집기가 본 것. 진실의 원천
      │
      ├→ commitments · workout_* · threads · routine_facts   ← 정확한 사실
      │
      └→ memory_chunks                                        ← 서술·벡터
```

## 왜 하나의 데이터베이스인가

회수 조건이 대개 섞여 있습니다 — "이 사람 관련 + 최근 30일 + 의미 유사".
벡터를 Qdrant 같은 별도 저장소에 두면 사람 id 와 날짜를 양쪽에 중복해서 들고
동기화해야 하고, 조각이 수만 건인 규모에서 그 비용은 검색 성능 이득보다
큽니다. [013_queries.sql](migrations/013_queries.sql) 의 `recall()` 이 그
세 조건을 한 번의 조회로 처리하는 모습입니다.

## 실행

`docker compose up` 이 알아서 합니다. `db-migrate` 가 성공으로 끝난 뒤에
agent 가 뜨도록 걸려 있습니다.

```bash
docker compose up -d postgres      # 데이터베이스만
docker compose run --rm db-migrate # 스키마 (몇 번 돌려도 안전)
docker compose exec postgres psql -U jarvis -d jarvis
```

`.env` 에 `JARVIS_POSTGRES_PASSWORD` 가 없으면 compose 가 그 자리에서
멈춥니다. 기본값을 두지 않은 것은 의도입니다.

## 마이그레이션

`migrations/` 의 파일을 이름순으로 돌리고 `schema_migrations` 에 기록합니다.
파일 적용과 기록이 한 트랜잭션이라, 중간에 실패하면 기록도 남지 않습니다.

`/docker-entrypoint-initdb.d` 를 쓰지 않은 이유는 그쪽이 **데이터 디렉터리가
빈 첫 부팅에만** 실행되기 때문입니다. 스키마를 한 번이라도 고칠 생각이라면
그건 마이그레이션이 아니라 일회용 시드입니다.

**이미 적용된 파일은 고치지 마세요.** 고쳐도 다시 돌지 않습니다. 새 번호를
붙이세요 — [014](migrations/014_raw_events_truncate_guard.sql) 가 002 의 구멍을
그렇게 메운 예입니다.

## 알아둘 것

**raw_events 는 추가 전용입니다.** UPDATE · DELETE · TRUNCATE 가 트리거로
막혀 있습니다. 이건 감사 기록 때문이 아니라 재생 때문입니다: 파생 테이블과
요약·임베딩을 전부 여기서 다시 만들 수 있어야, 나중에 추출 방식을 바꾸거나
메모리 레이어(Graphiti·mem0)를 얹는 결정을 미룰 수 있습니다.

정말 지워야 할 때는 껐다 켜세요. 끄는 것을 어렵게 만들면 아무도 트리거를
쓰지 않습니다.

```sql
alter table raw_events disable trigger raw_events_append_only;
alter table raw_events disable trigger raw_events_no_truncate;
-- ... 정리 ...
alter table raw_events enable trigger raw_events_append_only;
alter table raw_events enable trigger raw_events_no_truncate;
```

**멱등 키는 `(source, ext_id)` 입니다.** `ext_id` 가 NULL 인 행끼리는
충돌하지 않습니다(NULL 은 서로 다르게 취급됨). 커서를 만들 수 있는 수집기는
`ext_id` 를 채워서 재시작 후 재생이 중복 행을 만들지 않게 하세요.

**캘린더의 진실은 `on_date` + `start_time` 입니다.** `span` 은 트리거가
채우는 기계용 사본이니 직접 쓰지 마세요. 시간대는 데이터베이스에 박혀
있습니다 (`jarvis.timezone`, 기본 `Asia/Seoul`).

**겹치는 일정을 막지 않습니다.** 배제 제약을 걸지 않은 것은 의도입니다 —
일부러 겹쳐 잡는 일정이 있고, 사람이 직접 쓰는 경로는 결재를 거치지 않기로
한 제품입니다. 겹침은 막을 일이 아니라 알아차려서 제안할 일이므로
`conflicts_at()` / `conflicts_for()` 이 있습니다.

**임베딩 차원은 1536 입니다** (`text-embedding-3-small`). 모델을 바꾸면
차원이 바뀌므로, 한 테이블에 두 차원을 섞지 말고 새 마이그레이션으로
`memory_chunks` 를 바꿔 끼운 뒤 `raw_events` 에서 조각을 다시 만드세요.
`model` 컬럼은 섞였을 때 알아차리기 위한 것입니다.

## 룰 게이트가 부르는 것들

전부 [013_queries.sql](migrations/013_queries.sql) 에 있습니다. 정의를 한 곳에
묶은 이유는 재사용이 아니라, "충돌"이 무엇이고 "공백"이 며칠부터인지를
서비스마다 각자 정하면 제안하는 쪽과 화면에 보여주는 쪽이 다른 답을 내기
때문입니다.

| 함수 | 답하는 질문 |
|---|---|
| `conflicts_at(span, 제외id, 여유)` | 이 시간대에 겹치는 약속이 있나 |
| `conflicts_for(id, 여유)` | 이 약속이 다른 무엇과 겹치나 |
| `workout_gap_days()` | 마지막 운동에서 며칠 지났나 |
| `workout_routine_status` (뷰) | 어느 루틴이 가장 밀렸나 |
| `volume_by_group(일수)` | 이번 주에 하체 했나 |
| `fact_as_of(주제, 시각)` | 그때 루틴이 뭐였나 |
| `assert_fact(주제, 값, 시작)` | 새 사실 주장 (이전 행을 닫음) |
| `recall(벡터, 사람, 일수, 종류)` | 관련된 과거 맥락 |
| `open_threads(사람)` | 아직 결론이 없는 대화 |
| `suppressed(열쇠, 기간)` | 이 제안이 쿨다운 안인가 |
| `proposals_today()` | 오늘 몇 건이나 말을 걸었나 |

`assert_fact()` 는 같은 값을 다시 주장하면 아무것도 하지 않습니다. 수집기가
같은 상태를 매일 보고하는 것은 정상이고, 그때마다 사실이 끊겼다 이어지면
"언제 바뀌었나"의 답이 매일 어제가 됩니다. 반대로 과거 시각으로 주장하면
거부합니다 — 조용히 버리면 왜 반영이 안 되는지 알아낼 방법이 없습니다.

## 검증한 것

실제로 컨테이너를 띄워 확인했습니다.

- 벽시계 → 구간 변환이 KST 로 계산됨. 끝시각 없는 일정은 1시간으로 가정,
  종일 일정은 `span` 이 NULL (하루를 다 막으면 그날 모든 약속이 충돌로 잡힘)
- `conflicts_at` 이 겹치는 것만 찾고, 여유 30분을 주면 붙어 있는 일정도 잡음.
  `conflicts_for` 에서 자기 자신은 빠짐
- `assert_fact` 가 이전 행을 닫고, 같은 값 재주장은 같은 id 를 돌려주고,
  역순 주장은 거부. `fact_as_of` 가 과거 시점의 값을 돌려줌
- `recall` 이 사람 조건으로 좁혀짐 (조건 없이 2건 → 사람 걸고 1건)
- `suppressed` 가 열쇠별·기간별로 다르게 답하고, `proposals_today` 가
  `origin='chat'` 을 세지 않음
- UPDATE · 중복 `ext_id` · TRUNCATE 가 모두 거부됨
- 재실행 시 "변경 없음"

아직 안 한 것: 기존 `calendar.json` · `workout.json` · `thread.json` 을
이 테이블로 옮기는 마이그레이션. 각 기능을 서비스로 떼어낼 때 함께 합니다.
