# db

서비스들의 표와 이벤트 로그가 한 Postgres 안에 있습니다. 표는 서비스별로 나뉘어 있고
(접두사 `mind_` · `job_` · `context_` · `memory_` · `workout_` …), 각 서비스는 자기 역할로
자기 표만 씁니다(아래 "서비스별 역할").

```
event_outbox → events (events-svc)      ← 서비스가 낸 일. 구독자에게 배달
raw_events (추가 전용)                  ← 카드 알림 원문 (spending-svc)
input.records                           ← 일어난 일의 기록. 재생이 읽는 것 (아래)
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

## 기록 (input.records, 075)

to-be 구조의 입구가 될 표입니다. 지금은 재생할 것을 쌓는 데까지만 씁니다 - 판단은 아무도 여기서 읽지 않습니다.

| 기록 | 낸 곳 | 층 |
|---|---|---|
| `guard.query` · `guard.answer` | 에이전트 `/intercept`: 가드가 멈춘 요청과 그 답 | durable |
| `payment.requested` | 에이전트: 질의가 올린 결제 카드 | durable |
| `intent.answer` | 에이전트: 카드의 답(결제 카드는 durable, 그 밖은 제목에 사람의 일정이 있어 raw). 이유의 글은 남기지 않고 있었는지만 | durable / raw |
| `guard.refused` · `guard.flow` · `guard.passed` | 가드 `/intercept/records`: 묻지 않고 막은 것, 결제가 지나간 길, 묻지 않고 보낸 것 | durable / raw |
| `job.*` | `job_events` 트리거: 작업의 모든 줄. 손님의 작업은 넣지 않음 | raw |

- **쓰는 길은 `input.append` 하나입니다.** 표에 직접 쓸 권한은 어느 역할에도 없고, 받은 시각은 서버가 찍고, `(source, source_key)` 는 `input.record_keys` 가 한 번만 받습니다. 파티션 표의 유일 제약은 파티션 키를 포함해야 해서 키를 따로 둡니다.
- **재생은 `received_at, id` 순서입니다.** `occurred_at` 은 세상에서 일어난 시각이고 둘은 다를 수 있습니다.
- **고치지 않습니다.** UPDATE 는 트리거가 막고, DELETE 는 raw 층만 됩니다. durable 을 사람 단위로 지워야 할 때는 `records_guard` 트리거를 껐다 켭니다(raw_events 와 같은 이유). raw 층의 보존기간 집행은 input-svc 가 들어올 때 함께 옵니다 - 그때까지 `job.*` 은 `job_events` 와 같은 글을 담고, 그 글에는 카드로 받은 연락처가 있을 수 있습니다. 그래서 읽을 수 있는 역할은 소유자뿐이고, `scripts/harness export` 는 글을 빼고 내려받습니다.
- **달마다 나눕니다.** 2028-12 까지 미리 만들어 두었고 그 뒤는 기본 파티션이 받습니다.
- **기록이 나가지 않아도 일은 계속됩니다.** 에이전트의 쓰기는 줄에 넣기만 하고(`gocommon/record`), 트리거는 실패를 경고로만 남깁니다. 멈추면 상태 탭의 '에이전트의 기록'이 빨개집니다.

## 일정 충돌

[013_queries.sql](migrations/013_queries.sql) 의 `conflicts_at(span, 제외id, 여유)` 는 이 시간대에 겹치는
약속을, `conflicts_for(id, 여유)` 는 이 약속이 다른 무엇과 겹치는지를 답합니다. 정의를 한 곳에 둔 이유는
"충돌"이 무엇인지를 서비스마다 각자 정하면 제안하는 쪽과 화면에 보여주는 쪽이 다른 답을 내기 때문입니다.
calendar-svc 가 씁니다.

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
