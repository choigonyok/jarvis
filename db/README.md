# db

서비스들의 표와 이벤트 로그가 한 Postgres 안에 있습니다. 표는 서비스별로 나뉘어 있고
(접두사 `mind_` · `job_` · `context_` · `memory_` · `workout_` …), 각 서비스는 자기 역할로
자기 표만 씁니다(아래 "서비스별 역할").

```
event_outbox → events (events-svc)      ← 서비스가 낸 일. 구독자에게 배달
raw_events (추가 전용)                  ← 카드 알림 원문 (spending-svc)
commitments · workout_* · card_* · …     ← 각 서비스의 정확한 사실
mind_*                                   ← mind 의 세계 모델·관문·노트
```

**원문 보관(`raw_events`)은 가계부만 씁니다.** 처음에는 모든 수집기가 본 것을 여기에
추가 전용으로 쌓는 것이 관례였지만, 실제로는 원문이 각자 자리에 있습니다 - 카톡·iMessage
원문은 수집기의 SQLite(보존 730일), 대화 원문은 `mind_messages`(봉인, 보존 365일), 위치는
`context_raw`(30일). `raw_events` 는 카드사 알림 원문을, 파싱에 실패한 형식까지 남겨 나중에
다시 파싱하는 데 씁니다. 새 수집기가 원문을 남겨야 하면 자기 저장소에 두고 보존 기한을 정하세요.

사람·사실의 회수는 memory-svc(FalkorDB 그래프)가 맡습니다. 여기에 있던 회수용 벡터
표(`memory_chunks`)와 사람·사실 표는 049 에서 지웠습니다. 이미지가 pgvector 인 것은 새로
설치할 때 초기 마이그레이션(001·009·013)이 vector 확장을 만들기 때문뿐입니다.

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

사실·회수·제안 쿨다운 함수(`fact_as_of` · `assert_fact` · `recall` · `open_threads` ·
`suppressed` · `proposals_today`)는 그 표들과 함께 049 에서 지웠습니다. 제안의 쿨다운과
하루 몫은 mind 의 관문이 맡습니다.

## 검증한 것

실제로 컨테이너를 띄워 확인했습니다.

- 벽시계 → 구간 변환이 KST 로 계산됨. 끝시각 없는 일정은 1시간으로 가정,
  종일 일정은 `span` 이 NULL (하루를 다 막으면 그날 모든 약속이 충돌로 잡힘)
- `conflicts_at` 이 겹치는 것만 찾고, 여유 30분을 주면 붙어 있는 일정도 잡음.
  `conflicts_for` 에서 자기 자신은 빠짐
- UPDATE · 중복 `ext_id` · TRUNCATE 가 모두 거부됨
- 재실행 시 "변경 없음"

예전 JSON 파일(`calendar.json` · `workout.json` · `thread.json`)은 각 서비스로 옮겼고,
옮기는 데 쓴 일회성 import 서비스는 지웠습니다.

## 서비스별 역할 (050)

서비스는 소유자(`jarvis`)가 아니라 자기 역할(`svc_<서비스>`)로 붙는다. 역할은 050 이 비밀번호
없이 만들고, `scripts/db-roles` 가 맥미니 `.env` 의 `JARVIS_DB_PASSWORD_<서비스>` 로 비밀번호를
넣는다. 마이그레이션(db-migrate)과 백업만 소유자로 돈다.

**새 표를 만드는 마이그레이션은 같은 파일에서 그 표의 권한도 준다** - 쓰는 서비스에
`select, insert, update, delete`, 읽기만 하는 서비스에 `select`. 주지 않으면 그 서비스의 쿼리가
`permission denied` 로 실패한다. 이벤트를 내는 서비스는 `event_outbox` 에 `insert` 만 받는다.
