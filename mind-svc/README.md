# mind-svc

jarvis 를 **인지 루프** 하나로 다시 짠 실험 서비스. 기능마다 서비스를 붙이는 대신
인지의 단계로 나눈다.

```
감지 → 관측 로그 → 해석 → 세계 모델 → 주의·스킬 → 자율성 관문 → 행동
                              ↑                                │
                              └─────── 행동의 결과도 관측 ──────┘
```

| 층 | 패키지 | 하는 일 |
|---|---|---|
| 감지 | `internal/sensor` | 카톡·iMessage 수집기를 rowid 로 읽어 관측으로 남긴다. 판단하지 않는다. 원문은 content 저장소, 로그엔 ref 만 |
| 관측 로그 | `internal/obs` | 스택에선 events-svc(outbox → `POST /consume`), 로컬에선 프로세스 안의 로그. 다시 재생할 수 있다 |
| 해석 | `internal/interpret` | 대화를 **덩어리로**(방이 조용해지면) 읽고, 그 방의 일정·고리와 함께 LLM 에 준다. 변경마다 원문 줄 번호와 인용을 받고, 인용이 그 줄에 실제로 있어야 믿는다. 단어 목록이 없다 |
| 세계 모델 | `internal/world`, `internal/store` | 일정(plan)과 열린 고리(loop). 모든 변경은 누가·무엇에서 무엇으로·어떤 근거로를 남긴다. 바뀌면 주의 층에 알린다 |
| 주의 | `internal/attention` | 변화를 잠깐 가라앉혀 묶어서(제안→확정→장소 변경이 1분 안이면 한 번) 주인 스킬에 넘긴다. 타이머, 아침 시각도 계기 |
| 스킬 | `internal/skill` | 일정(캘린더와 맞추기, 하루 전·두 시간 전 확인), 열린 고리(기한 전 알림), 아침 브리핑. 의도만 내고 직접 하지 않는다 |
| 자율성 관문 | `internal/gate` | 사람에게 닿는 유일한 문. 바로 실행 / 실행 후 알림(되돌리기) / 묻기 / 요약 / 버림. 승인·거절·되돌림으로 신뢰 단계를 배우고, 하루 알림 한도를 지킨다 |
| 행동 | `internal/act` | 캘린더 추가·수정·삭제. 모두 미리보기·실행·되돌리기를 갖춘다 |
| 평가 | `internal/eval` | 정답이 적힌 대화 시나리오. 스크립트로(결정적) 또는 실제 모델로 돈다 |

설계 개요: "jarvis 인지 루프 설계" 페이지. 지금 운영 중인 서비스와는 따로 돈다 — 운영
compose 에는 `mind` 프로필로만 들어 있어 평소 배포에는 뜨지 않는다.

## 로컬에서 돌려 보기

```sh
scripts/mind dev      # 메모리 모드: DB·Docker 없이. http://localhost:8102
scripts/mind stack    # Postgres + events-svc + mind (docker-compose.mind.yml)
scripts/mind down -v  # stack 내리고 데이터까지 지우기
scripts/mind test     # go test ./... (모델 없이, 결정적)
scripts/mind eval     # 평가 시나리오를 실제 모델로
```

LLM 키는 `MIND_ANTHROPIC_API_KEY` 가 환경에 있으면 그것을, 없으면 맥미니 `.env` 의
`MEMORY_ANTHROPIC_API_KEY` 를 **실행할 때만** ssh 로 읽어 넘긴다(파일로 복사하지 않는다).
키가 없어도 뜨지만 대화를 해석하지 않고 쌓아만 둔다.

콘솔(`/`)에서:

1. 왼쪽 아래에서 상대로 "이번 주말에 강릉 갈까?", 나로 "그러자!" 를 보낸다
2. 몇 초 뒤(시뮬레이터는 4초 조용하면) 해석되어 가운데 세계 모델에 **확정된 일정**이 생긴다.
   카드를 누르면 근거 인용과 함께 이력이 보인다
3. 오른쪽에 "캘린더에 추가" 질문이 뜬다 → 승인하면 메모리 캘린더에 들어가고 일정에
   캘린더 ✓, 하루 전 타이머가 걸린다
4. 상대로 "춘천으로 가자" 를 보내면 **같은 일정**의 장소가 바뀌고 "캘린더 수정" 이 뜬다
5. 메모리 모드에선 `+1일` 로 시계를 옮겨 하루 전 확인·기한 알림을 볼 수 있다
6. 세 번 연속 승인하면 그다음부터는 묻지 않고 바로 하고 알린다. 되돌리면 다시 묻는다

## 평가

`internal/eval/scenarios/*.json` 이 정답지다. 대화 한 단계마다 기대하는 세계 모델·의도·캘린더를
적고, `script` 에 올바른 모델 응답을 적어 둔다.

- `go test ./internal/eval` — script 로 돌린다. 모델 주변(근거 검증, 세계 모델, 스킬, 관문,
  캘린더, 타이머)이 맞는지 본다
- `scripts/mind eval` — 실제 모델로 돌린다. 프롬프트나 모델을 바꾸면 여기서 먼저 확인한다
  (2026-10-11 Haiku 5.5: 12개 중 12개를 3번 연속, 회당 약 $0.007)

시나리오: 강릉→춘천 장소 변경, "언제 한번 보자"는 약속 아님, 나와의 채팅 확정 메모,
확정 후 취소, 내가 한 약속(열린 고리)과 기한 알림, 잡담, 남의 일정, 시간 변경, 한 대화의
일정 두 개, "다음 주 화요일", 거절된 제안, 부탁받은 일.

`POST /api/replay {after, limit}` 은 관측 로그의 한 구간을 **새 메모리 루프**에 그대로
다시 흘려(로그의 시각대로 방이 조용해지면 읽는다) 무엇을 믿고 하려 했는지 보고한다. 실제
세계 모델·캘린더·알림은 건드리지 않는다.

## API

```
GET  /health
POST /consume                          events-svc 가 관측을 전달 (토큰 필요)
GET  /                                 콘솔
GET  /api/snapshot                     콘솔이 보는 전부
POST /api/simulate                     {room, roomName, sender, mine, text, at?}
POST /api/read                         {room?} 조용해지기를 기다리지 않고 지금 해석
POST /api/settle                       가라앉는 중인 변화를 지금 스킬에
POST /api/tick                         {name: "morning"}
POST /api/clock                        {advance: "24h"} 메모리 모드에서 시계 옮기기
POST /api/intents/{id}/approve|reject|undo
GET  /api/entities/{id}/changes        근거가 달린 이력
POST /api/replay                       {after, limit}
```

`/api` 와 콘솔은 `MIND_UI_OPEN=true`(스택에서 루프백에만 열 때)거나 메모리 모드면 토큰 없이,
아니면 `Authorization: Bearer $API_TOKEN`.

## 설정

| 변수 | 기본값 | |
|---|---|---|
| `DATABASE_URL` | (없음) | 없으면 메모리 모드 |
| `EVENTS_URL` | (없음) | 스택의 관측 로그. 없으면 프로세스 안의 로그 |
| `MIND_SELF_URL` | `http://mind:8102/consume` | events-svc 가 전달할 곳 |
| `KAKAOTALK_URL`, `IMESSAGE_URL` | (없음) | 있으면 감지기가 돈다. 첫 시작은 `MIND_BACKFILL`(6h)만 읽는다 |
| `MIND_ANTHROPIC_API_KEY` | | `MIND_MODEL`(claude-haiku-5-5), `MIND_DAILY_USD`(1.0) |
| `MIND_CALENDAR` | `memory` | `http` 면 calendar-svc(`CALENDAR_URL`)에 실제로 쓴다 |
| `MIND_CONTENT_KEY` | (없음) | base64 32바이트. 있으면 메시지 원문을 AES-GCM 으로 봉해 저장 |
| `MIND_IDLE`, `MIND_IDLE_SELF`, `MIND_IDLE_SIM` | 3m, 45s, 4s | 방이 이만큼 조용하면 읽는다 (나와의 채팅은 짧게) |
| `MIND_SETTLE` | 메모리 3s / 스택 1m | 변화를 묶는 시간 |
| `MIND_EXCLUDE_ROOMS` | 카드·은행·알림·광고… | 읽지 않을 방 이름 정규식 |
| `MIND_OWNER_NAME` | 나 | 프롬프트의 "나" |

저장소는 마이그레이션 `035_mind.sql`.

## 아직 아닌 것

- 스킬은 일정·열린 고리·아침 브리핑 셋. 재무(비중 이탈→리밸런싱 계획), 관계(사람 프로필)는 다음
- 맥락 계기(집 도착 등 위치), 메일 감지기
- 지금 운영 중인 agent 의 제안 엔진(`memory.plan` 경로)과 notify 의 결재 카드는 그대로 돈다.
  이 루프가 검증되면 관문 하나로 합친다
- 콘솔은 jarvis 웹과 따로다. 합칠 때 대화창의 카드·타임라인으로 옮긴다
