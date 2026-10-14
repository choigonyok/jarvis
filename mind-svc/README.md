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
| 해석 | `internal/interpret` | 대화를 **덩어리로**(방이 조용해지면) 읽고, 그 방의 일정·고리와 장기 기억이 그 사람에 대해 아는 것을 함께 LLM 에 준다. 변경과 기억할 사실마다 원문 줄 번호와 인용을 받고, 인용이 그 줄에 실제로 있어야 믿는다. 단어 목록이 없다 |
| 세계 모델 | `internal/world`, `internal/store` | 일정(plan)과 열린 고리(loop). 모든 변경은 누가·무엇에서 무엇으로·어떤 근거로를 남긴다. 바뀌면 주의 층에 알린다. 일정에는 종류(데이트·캠핑·회식…)·장소 종류·동네가 붙는다 |
| 지금 상황 | `internal/world/now.go` | 진행 중일 수 있는 일정(끝 시각이 없으면 같은 종류의 평균 길이, 없으면 보통 길이), 최근 3시간의 위치 단서(결제·사진), 그리고 "어디서 누구와"의 추정과 신뢰도를 **규칙으로** 계산한다(LLM 없음). 같은 동네·장소의 단서는 올리고 다른 곳의 단서는 내린다. `GET /now` 로 에이전트가 **매 턴 시스템 프롬프트**에 넣고, 0.5 미만이면 추측하지 말고 묻게 한다. 운영자가 jarvis 에게 "집에 왔어"라고 하면 해석기가 그 일정을 끝내 추정이 고쳐진다 |
| 발생·링크 | `mind_occurrences`, `mind_links` | 구조화 소스에서 규칙으로 읽은 일(결제·상품·사진·운동·캘린더·이체)과 일 사이의 관계. 원본의 색인이다 |
| 장기 기억 | `internal/memory`, `internal/mind/remember.go` | 일정·고리가 아닌 기억할 사실(취향, 관계, 근황)을 **근거 장부**(`mind_facts`)에 쓰고, 장부가 outbox 가 되어 memory-svc(`/facts`, Graphiti)로 보낸다. 기억의 판단 - 어느 노드에 묶였는지, 이미 알던 것인지, 무엇을 끝냈는지 - 이 같은 행에 돌아오고, 이름 → 노드는 **사람 ID 표**(`mind_aliases`)에 남는다 |
| 회상 | `internal/mind/recall.go` | 세계 모델(정확 조회)과 장기 기억(사람 중심 검색, 프로필)을 함께 읽는 한 곳. 해석기는 방의 사람에 대해, 일정 알림은 함께하는 사람에 대해 읽는다 |
| 주의 | `internal/attention` | 변화를 잠깐 가라앉혀 묶어서(제안→확정→장소 변경이 1분 안이면 한 번) 주인 스킬에 넘긴다. 타이머, 아침 시각도 계기 |
| 스킬 | `internal/skill` | 일정(캘린더와 맞추기, 하루 전·두 시간 전 확인), 열린 고리(기한 전 알림), 아침 브리핑. 의도만 내고 직접 하지 않는다 |
| 자율성 관문 | `internal/gate` | 사람에게 닿는 **유일한** 문. 스킬의 의도도, 에이전트 판단 턴의 제안(`POST /api/intents`)도 여기를 지난다. 바로 실행 / 실행 후 알림(되돌리기) / 묻기 / 요약 / 버림. 주제 장부(최근 제안 3일·승인 7일·거절 14일 동안 같은 주제는 다시 묻지 않음), 하루 카드 5장·알림 8개 한도, `(스킬, 맥락)`별 승인율(`mind_policy_stats`)로 신뢰 단계를 배운다. 카드는 `확신 × 승인율 × 가치 > 방해 비용`일 때만 울리고, 아니면 저녁 요약. 울리는 질문은 `mind.card` 이벤트로 에이전트에 가서 결재 카드가 되고, 답은 다시 여기로 온다 |
| 행동 | `internal/act` | 캘린더 추가·수정·삭제, 실행 없는 계획 확인(`note`). 모두 미리보기·실행·되돌리기를 갖춘다. 캘린더에 같은 날 같은 제목이 이미 있으면 묻지 않고 연결만 한다 |
| 평가 | `internal/eval` | 정답이 적힌 대화 시나리오. 스크립트로(결정적) 또는 실제 모델로 돈다 |

설계 개요: "jarvis 인지 루프 설계" 페이지, 기억과의 관계는 "Graphiti 엔진 구성"(A 안) 페이지.
mind 는 유일한 추출기이고, 사람 묶기·모순 정리·요약·검색은 memory-svc 의 Graphiti 가 맡는다.
운영 compose 에는 `mind` 프로필로만 들어 있어 평소 배포에는 뜨지 않는다.

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

`18~21` 은 지금 상황(S4)이다: 데이트 중 같은 동네 결제·사진이면 0.85, 단서가 없거나 다른 동네면
0.5 미만으로 묻기, 영화관이면 근처 가게인지 확인, 운영자의 정정으로 추정이 사라짐. 시나리오의 `occur`
는 감지기가 남길 발생, `now` 는 그 단계 뒤의 지금 상황이다. `MIND_EVAL_ONLY=<파일 이름 정규식>` 으로
실제 모델 평가를 일부만 돌린다.

`17-silence-gate` 는 관문의 침묵(S11)을 본다: 거절한 주제는 14일 동안 다시 묻지 않고, 하루 카드
5장을 넘지 않고, 확신이 낮으면 울리지 않는다. 시나리오의 `submit` 단계는 에이전트 판단 턴의 제안,
`cards`·`noCards`·`dropped` 는 그 단계에서 나간 카드와 관문이 버린 것이다.

시나리오: 강릉→춘천 장소 변경, "언제 한번 보자"는 약속 아님, 나와의 채팅 확정 메모,
확정 후 취소, 내가 한 약속(열린 고리)과 기한 알림, 잡담, 남의 일정, 시간 변경, 한 대화의
일정 두 개, "다음 주 화요일", 거절된 제안, 부탁받은 일.

`13-facts`, `14-self-fact` 는 기억할 사실을 본다: 상대가 말한 취향·근황은 기억하고 농담은 넘기는지,
나와의 채팅에서 산 물건은 사실로, 예약은 일정으로 나누는지. 스크립트 실행에서는 장기 기억 대신
프로세스 안의 가짜(`memory.Fake`)가 받는다.

`POST /api/replay {after, limit}` 은 관측 로그의 한 구간을 **새 메모리 루프**에 그대로
다시 흘려(로그의 시각대로 방이 조용해지면 읽는다) 무엇을 믿고 하려 했는지 보고한다. 실제
세계 모델·캘린더·알림은 건드리지 않는다.

## API

```
GET  /health
POST /consume                          events-svc 가 관측을 전달 (토큰 필요)
GET  /                                 콘솔
GET  /api/snapshot                     콘솔이 보는 전부
GET  /now                              지금 상황(규칙으로 계산한 요약과 추정, 에이전트가 매 턴 읽는다)
POST /api/simulate                     {room, roomName, sender, mine, text, at?}
POST /api/read                         {room?} 조용해지기를 기다리지 않고 지금 해석
POST /api/settle                       가라앉는 중인 변화를 지금 스킬에
POST /api/tick                         {name: "morning"}
POST /api/clock                        {advance: "24h"} 메모리 모드에서 시계 옮기기
POST /api/intents                      외부(에이전트 판단 턴)의 의도: {skill, kind, topic, context, title, body, reason, confidence, value, payload}
POST /api/intents/{id}/approve|reject|undo   reject 는 {reason}
POST /api/policy/rebuild               승인율 통계를 의도 이력에서 다시 센다
GET  /api/entities/{id}/changes        근거가 달린 이력
POST /api/replay                       {after, limit}
GET  /api/recall?q=&person=            세계 모델 + 장기 기억에서 아는 것
POST /api/facts/flush                  장부에서 기다리는 사실을 지금 기억으로
```

`/api` 와 콘솔은 메모리 모드에서만 토큰 없이 열린다. 스택에서는 `Authorization: Bearer $API_TOKEN`,
브라우저는 한 번 `/?token=$API_TOKEN` 으로 열면 HttpOnly 쿠키를 받고 주소창에서 토큰을 지운다.
스택은 `API_TOKEN` 이 비면 뜨지 않는다(로컬 시험만 `API_TOKEN_OPTIONAL=true`).

## 설정

| 변수 | 기본값 | |
|---|---|---|
| `DATABASE_URL` | (없음) | 없으면 메모리 모드 |
| `EVENTS_URL` | (없음) | 스택의 관측 로그. 없으면 프로세스 안의 로그 |
| `MIND_SELF_URL` | `http://mind:8102/consume` | events-svc 가 전달할 곳 |
| `KAKAOTALK_URL`, `IMESSAGE_URL` | (없음) | 있으면 감지기가 돈다. 첫 시작은 `MIND_BACKFILL`(6h)만 읽는다 |
| `CHAT_URL` | (없음) | 있으면 운영자와 jarvis 의 대화(chat-svc)도 대화방 하나(`chat:owner`)로 읽는다 |
| `MIND_MEMORY_URL` | (없음) | memory-svc. 없으면 메모리 모드는 프로세스 안의 가짜 기억, 스택은 사실을 장부에만 쌓는다 |
| `MIND_PLAN_EVENTS` | `false` | `true` 면 확정된 일정을 `memory.plan` 이벤트로 알린다(에이전트의 제안 엔진이 캘린더 카드로) |
| `MIND_QUIET` | `true`(스택) | 스킬·관문의 알림을 notify-svc 로 보내지 않고 콘솔에만 둔다 |
| `MIND_ANTHROPIC_API_KEY` | | `MIND_MODEL`(claude-haiku-5-5), `MIND_DAILY_USD`(1.0) |
| `MIND_CALENDAR` | `memory` | `http` 면 calendar-svc(`CALENDAR_URL`)에 실제로 쓴다 |
| `MIND_CONTENT_KEY` | (없음) | base64 32바이트. 있으면 메시지 원문을 AES-GCM 으로 봉해 저장 |
| `MIND_IDLE`, `MIND_IDLE_SELF`, `MIND_IDLE_SIM` | 3m, 45s, 4s | 방이 이만큼 조용하면 읽는다 (나와의 채팅은 짧게) |
| `MIND_SETTLE` | 메모리 3s / 스택 1m | 변화를 묶는 시간 |
| `MIND_EXCLUDE_ROOMS` | 카드·은행·알림·광고… | 읽지 않을 방 이름 정규식 |
| `MIND_OWNER_NAME` | 나 | 프롬프트의 "나" |

저장소는 마이그레이션 `035_mind.sql`, 근거 장부와 사람 ID 표는 `038_mind_memory.sql`,
관문의 승인율은 `039_mind_policy.sql`(의도 이력의 집계라 `POST /api/policy/rebuild` 로 다시 만든다).

## 아직 아닌 것

- 스킬은 일정·열린 고리·아침 브리핑 셋. 재무(비중 이탈→리밸런싱 계획), 관계(사람 프로필)는 다음
- 맥락 계기(집 도착 등 위치), 메일 감지기. 위치 센서는 없다: 지금 상황의 위치 단서는 결제 가맹점과
  사진뿐이고, Immich 의 장소는 영어 시·군 단위라("Gapyeong") 동 단위로는 맞추지 못한다
- 근처 가게 검색(지도 API)은 이번에 하지 않았다(2026-10-15 결정)
- (2026-10-15 합침) 관문은 하나다. 에이전트의 제안 엔진은 더 이상 스스로 카드를 열지 않는다:
  mind 가 보낸 `mind.card` 를 결재 카드로 보여 주고(승인 → `mind.approve`, 거절 → 이유와 함께
  `/reject`), 자산 비중 이탈 같은 판단 턴의 제안도 `POST /api/intents` 로 이 관문에 넘긴다.
  `memory.plan` 은 아무도 구독하지 않는다
- 구조화 데이터(캘린더·가계부·자산·운동·작업·사진)는 아직 memory-svc 가 직접 모은다. 수집을 한 길로
  모으는 것(이것들도 관측 로그 → mind)은 다음
- 콘솔은 jarvis 웹과 따로다. 합칠 때 대화창의 카드·타임라인으로 옮긴다
