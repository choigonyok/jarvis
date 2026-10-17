# jarvis

개인 AI 어시스턴트. 1:1 스레드 하나에서 대화하고, 상태를 바꾸는 일은 실행 전에 승인을 받습니다.
**Claude Code 구독으로 과금됩니다** (API 크레딧 아님).

```
web (Next.js, Cloudflare Workers)   jarvis.choigonyok.com, Cloudflare Access 뒤.
      │  사람이 읽고 결재하는 화면. 자격증명은 서버측에만, 전부 프록시(src/lib/proxy.ts)
      │  서비스 바인딩(GATEWAY) + JARVIS_EDGE_TOKEN
      ▼
edge (gateway Worker)  jarvis-be.choigonyok.com. 경로 허용 목록, 토큰을 서비스용(JARVIS_API_TOKEN)으로
      │  바꿔 붙임. 공개 주소는 폰(OwnTracks·단축어)과 화면 탭의 VNC 소켓만 직접 씀
      │  Workers VPC → cloudflared(tunnel)
      ▼
맥미니 docker compose (networks: default · data · browser)

agent (Go) :8080      결정하는 곳 - 대화, 권한 게이트, 결재 카드
      ├─ core/proposal        ← 제안과 결재. 이 시스템의 심장
      ├─ MCP /mcp             ← 권한 질의를 제안으로 바꾸는 어댑터
      ├─ MCP /mcp/<모듈>      ← 모듈이 모델에게 내주는 도구
      ├─ SSE /events          ← 라이브 스트림과 진행 상태 (영속되지 않음)
      ├─ jobs 스케줄러·주문 조회·브라우저 로그인 확인 ← 대화가 없을 때만 브라우저를 씀
      └─ exec: claude -p ...  ← 구독으로 과금, 실제 실행
agent-guest :8080     손님 계정의 agent (캘린더만, DB 에 닿지 못함)

mind-svc :8102        인지 루프. 감지 → 해석 → 세계 모델 → 노트·예측·성찰 → 스킬 →
                      하나뿐인 관문(원장·예산·신뢰도). 카드는 mind.card → agent 결재 카드

기능 서비스           각자 디렉터리 하나, 이미지 하나, 상태는 Postgres 에(서비스별 역할 svc_*)
      ├─ chat-svc      :8094  대화 기록
      ├─ calendar-svc  :8093  일정 (uniple 공유 캘린더, postgres 백엔드는 롤백용)
      ├─ workout-svc   :8091  운동 기록 (리비전·세션 단위 쓰기, 부위 판정)
      ├─ assets-svc    :8092  업비트·KIS·금 (주문 가능한 키가 여기에만), 목표 비중·매매 계획
      ├─ spending-svc  :8095  가계부 (카드사 카톡 알림 → 분류·월 합계·예산)
      ├─ jobs-svc      :8098  맡긴 작업의 기록 (실행은 agent 스케줄러)
      ├─ context-svc   :8103  위치·현재 상태 (OwnTracks·단축어·맥 수집기)
      ├─ events-svc    :8101  이벤트 로그 + outbox + 구독 배달
      ├─ notify-svc    :8097  알림함·웹 푸시·저녁 요약, 알림 분류 목록
      ├─ status-svc    :8096  상태 탭 (기능별 판정, 읽기만)
      └─ memory-svc    :8100  장기 기억 (Graphiti + FalkorDB)
수집기                kakaotalk-client :8090 · imessage-client :8099 (평문 사본, 보존 730일)
browser :8931         에이전트의 Chromium (browser 네트워크에만, 결제 가드 guard.py)

postgres (data 네트워크)  서비스들의 표와 이벤트 로그 · falkordb (data) 기억 그래프
gocommon/             Go 공통 코드 (이벤트 outbox, 수집기 인증·보존)
```

각 서비스의 README 에 경계를 그은 이유와 검증한 것이 있습니다:
[chat-svc](chat-svc/README.md) · [calendar-svc](calendar-svc/README.md) ·
[workout-svc](workout-svc/README.md) · [assets-svc](assets-svc/README.md) ·
[spending-svc](spending-svc/README.md) ·
[status-svc](status-svc/README.md) · [context-svc](context-svc/README.md) ·
[mind-svc](mind-svc/README.md) ·
[db](db/README.md)

**에이전트에 남은 것과 나간 것.** 저장소는 전부 나갔고, 결정은 전부 남았습니다.
승인 카드·도구 스키마·`Reversible` 선언·SSE·진행 상태는 대화와 게이트의
속성이라 에이전트의 것입니다. 제안 저장소도 남았습니다 — 그건 게이트의 다른
반쪽이고, 결정이 날 때까지 반환하지 않는 핸들러와 한 몸입니다.

제안(proposal)은 저장되는 객체이지 대화의 속성이 아닙니다. 그래서 **대화 없이도
제안이 열립니다** — 생산자는 둘입니다: 대화 중의 권한 게이트(아래), 그리고 mind 의
관문이 보낸 카드(`mind.card`, 카톡에서 확정된 약속을 캘린더에 넣을지, 비중이 벗어난
포트폴리오를 리밸런싱할지 같은 것). 먼저 말을 꺼낼지의 판단과 그 원장(최근에 물었나,
거절했나, 오늘 몫을 다 썼나)은 mind 에만 있고, agent 는 카드를 띄우고 거절을 mind 에
알릴 뿐입니다.

## 승인 게이트가 걸리는 자리

Claude Code는 `--permission-prompt-tool`로 지정된 MCP 도구에 권한을 물어봅니다.
그 도구가 이 에이전트의 `/mcp`이고, 핸들러는 **결정이 날 때까지 반환하지 않습니다.**
문서상 이 대기는 무기한 허용됩니다 — *"The callback can stay pending indefinitely."*

- 승인 → `{"behavior":"allow"}` → Claude Code가 실행
- 반려 → `{"behavior":"deny","message":"..."}` → 실행되지 않고, 반려 사실이 모델에 전달
- 무응답 → `JARVIS_APPROVAL_TIMEOUT` 후 반려 처리

CLI에 넘기는 `MCP_TIMEOUT`/`MCP_TOOL_TIMEOUT`은 **밀리초**입니다(CLI 기본 30000).
카드가 떠 있는 동안 도구 호출은 그 예산을 쓰고 있으므로, 예산은 사람이 기다릴 수 있는
시간(`JARVIS_APPROVAL_TIMEOUT` + 여유)보다 커야 합니다. 초 단위로 넘기면 실제 한도가
1.8초가 되어 **승인은 됐는데 실행은 안 되는** 상태가 됩니다.

`JARVIS_ALLOWED_TOOLS`에 있는 도구는 카드를 거치지 않습니다. **읽기 전용만 넣으세요.**
기본값의 `mcp__calendar__list_events`가 그 예입니다. 캘린더 쓰기는 일부러 빠져 있습니다.
켜진 모듈의 조회 도구(자산·가계부·기억·위치·메시지·로그인 목록)는 모듈이 선언한 목록(`Tools`)에서
에이전트가 직접 더하므로, 조회 도구가 새로 생겨도 이 값이나 맥미니 `.env`를 고칠 필요가 없습니다.

## 모듈

기능은 모듈로 붙였다 뗍니다. 하나의 큰 `Plugin` 인터페이스 대신 셋으로 나뉘어 있고,
모듈은 **자기가 실제로 하는 것만** 구현합니다 (`internal/core/module`).

| 인터페이스 | 뜻 | 캘린더 |
|---|---|---|
| `Source` | 세상을 보고 이벤트를 흘린다 | ✗ |
| `Actuator` | 세상을 바꾼다 (+ 승인 카드를 직접 그린다) | ✓ |
| `ContextSource` | 판단에 쓸 사실을 내놓는다 | ✓ |

추가는 `main.go`의 `modules.Add(...)` 한 줄, 제거는 그 줄을 지우는 것입니다.
레지스트리가 타입 단언으로 분류하므로 빈 메서드를 채울 일이 없습니다.

모듈이 선언한 동작(`action.Spec`)은 세 곳을 한꺼번에 먹입니다 — MCP 도구 정의,
승인 카드, 그리고 자동 실행 가능 여부(`Reversible`). **되돌릴 수 없는 동작은 신뢰도가
아무리 높아도 카드를 거칩니다.**

### 캘린더

- 일정은 `calendar-svc`(Postgres)에 있고, 주소는 `JARVIS_CALENDAR_URL`입니다.
  전에는 워크스페이스 바깥의 JSON 파일이었는데, 이유는 같습니다 — 안에 두면
  CLI의 `Read`/`Write` 도구가 모듈의 스키마와 승인 카드를 우회하는 두 번째 문이
  됩니다. 이제 에이전트의 파일시스템에 캘린더가 **아예 없습니다.**
- 모델에겐 도구 4개만 보입니다: `list_events`(무승인) / `create_event` /
  `update_event` / `delete_event`.
- 시간대는 `TZ`(기본 `Asia/Seoul`)입니다. 컨테이너 기본값인 UTC로 두면 한국 시간
  자정~09시 사이에 "오늘"이 하루 밀립니다. zoneinfo는 바이너리에 내장돼 있어
  이미지에 `tzdata`를 깔 필요가 없습니다.
- 날짜는 `YYYY-MM-DD`, 시각은 `HH:MM`만 받습니다. "다음 주 화요일"은 저장 단계에서
  거부되고, 변환은 모델이 합니다 — 오늘 날짜는 **매 턴** 시스템 프롬프트에 주입됩니다
  (부팅 시 한 번이 아니라).

### 자산

- 읽기 전용입니다. 모듈이 `ContextSource` 만 구현하고 `Actuator` 가 없으므로
  승인할 동작 자체가 없습니다. 주문 가능한 키는 `assets-svc` 에만 있고, 에이전트는
  `JARVIS_ASSETS_URL` 로 GET 만 칩니다.
- 모델에게 보이는 도구는 셋이고 전부 무승인입니다: `get_portfolio`(총액·종목·원금 대비·
  기간 변동) / `get_allocation`(목표 비중 대비와 옮길 금액) / `get_history`(일별 스냅샷).
- 목표 비중 계산은 `assets-svc/src/target.ts` 한 곳에 있습니다. 웹과 에이전트가 같은
  `/portfolio` 의 `allocation` 을 읽으므로, 새 배당·금 종목은 거기에만 추가하면 됩니다.
- 캘린더와 달리 부팅 때 서비스를 확인하지 않습니다. 증권사 장애가 대화까지 막을 이유가
  없고, 조회 실패는 도구 오류로 모델에게 전달됩니다.

### 가계부

- 자산과 같은 읽기 전용 모듈입니다. 도구 `get_spending`(월 합계·지난달 같은 날까지 비교·
  예산 속도·분류·정기 결제·건별 내역) 하나이고 무승인입니다. 분류 변경과 예산은 화면에서 합니다.
- **백그라운드 주문 조회.** 쿠팡 결제가 새로 들어오면 에이전트가 대화와 분리된
  Claude 세션(`claudecode.Background`)을 띄워 브라우저로 주문목록을 읽고 상품별로 나눕니다.
  이 세션에는 내장 도구(셸·파일)가 없고, 브라우저 읽기·클릭과 결과 기록 도구 두 개만 허용되며
  (`--permission-mode dontAsk`), 나머지는 묻지 않고 거부됩니다. 글자 입력이 없어 로그인·결제를
  할 수 없습니다. 기록 도구는 `/mcp/spending-enrich` 에 있지만 대화의 MCP 설정에는 들어가지
  않습니다. 사람이 말을 걸면 즉시 멈추고 나중에 다시 합니다 — 브라우저는 창 하나를 같이 씁니다.
  `JARVIS_ENRICH=false` 로 끕니다.
  백그라운드 작업이 브라우저를 쓰는 동안 결제 안전장치(`/intercept`)에 걸린 요청은 카드를 띄우지
  않고 그 자리에서 결정합니다 — 그 카드 앞에는 아무것도 시작하지 않은 사람이 앉아 있기 때문입니다.
  `JARVIS_BACKGROUND_LOGIN_HOSTS`(기본 `nid.naver.com`)는 허용하고(브라우저에 저장된 비밀번호로 로그인
  버튼만 누르게 함), 나머지 — 모든 결제 호스트 — 는 거부합니다.
  예외는 작업(jobs)입니다. 대화는 브라우저 일을 전부 작업으로 넘기므로, 운영자가 맡긴 구매의 결제도
  작업에서 일어납니다. 작업의 실행이 결제하기에 닿으면 그 작업에 묶인 결제 카드를 올리고 요청은 막으며,
  작업은 `wait_payment` 로 그 카드를 기다립니다. 승인하면 그 사이트가 그 작업에만 30분 열리고(대화나
  다른 작업에는 열리지 않음) 작업이 깨어나 결제를 다시 진행합니다 — 쿠페이 비밀번호는
  `browser_enter_payment_pin` 이 넣습니다. 쿠팡 주문서는 결제수단 선택도 결제하기와 같은 주소로 보내므로,
  가드는 본문의 이벤트(`order` 만 결제)로 가릅니다(`browser/payment-hosts.json` 의 `pass`).

## 실행

백엔드는 맥미니의 docker compose, 웹과 게이트웨이는 Cloudflare Workers 입니다.
배포 절차와 비밀값 위치는 [DEPLOY.md](DEPLOY.md)에 있습니다.

```bash
npx @anthropic-ai/claude-code setup-token    # 1년짜리 토큰 발급
cp .env.example .env                          # CLAUDE_CODE_OAUTH_TOKEN 채우기
openssl rand -hex 24                          # JARVIS_POSTGRES_PASSWORD 에 넣기
scripts/db-roles                              # 서비스별 DB 역할 비밀번호(.env 에 생성)
scripts/mini up                               # 맥미니로 보내고 docker compose up -d --build
(cd web && npm run cf:build && npm run cf:deploy)
(cd edge && npx wrangler deploy)
```

`JARVIS_POSTGRES_PASSWORD` 가 없으면 compose 가 그 자리에서 멈춥니다. 기본값을
두지 않은 것은 의도입니다 — 리포지터리에 적힌 비밀번호는 비밀번호가 아닙니다.
서비스별 역할 비밀번호(`JARVIS_DB_PASSWORD_*`)도 마찬가지입니다.

스키마는 `db-migrate` 가 부팅 때 적용하고, 나머지 서비스는 그것이 성공으로 끝난
뒤에 뜹니다. 몇 번 돌려도 안전합니다. 백업은 `scripts/backup`(매일 04:30, 복원 시험 포함).

`ANTHROPIC_API_KEY`가 환경에 있으면 **에이전트가 부팅을 거부합니다.** 조용히 API
종량 과금으로 새는 것을 막기 위한 의도적인 동작입니다.

### 구독 사용량

대화는 CLI 세션 하나를 `--resume` 으로 이어 가고, 요청마다 도구 정의·지시·대화 기록 전체가
실립니다. 앞부분이 직전 요청과 같으면 캐시에서 읽고(입력의 0.1배), 처음 달라진 곳부터는 다시
씁니다(5분 캐시 1.25배, 1시간 캐시 2배). 그래서:

- **지시는 고정합니다.** 시각과 mind 의 지금 상황 같은 값은 메시지 앞에만 붙이고, 직전과 같은
  상황은 다시 붙이지 않습니다. 규칙 문구와 취향(1500자 상한)은 지시에 있고, 취향이 바뀌면
  새 세션이 됩니다.
- **세션을 바꿉니다.** 운영자가 친 메시지가 마지막 턴 뒤 `JARVIS_SESSION_IDLE`(5분 캐시면 30분,
  1시간 캐시면 1시간) 넘게 지나서 왔거나 기록이 `JARVIS_SESSION_MAX_TOKENS`(5만)를 넘으면,
  최근 대화(약 6천 자)만 붙여 새 세션을 엽니다. 기록이 `JARVIS_SESSION_MIN_TOKENS`(2만)보다
  작거나, 최근 두 시간 안에 이 대화가 올린 카드(대화·결제)가 떠 있으면 그대로 둡니다.
- **캐시 수명**은 `JARVIS_CACHE_TTL`(대화, 기본 `5m`, 또는 `1h`)이고 백그라운드는 늘 5분입니다.
  다음 질문이 대개 5분~1시간 뒤에 오면 `1h` 가 쌉니다.
- **도구를 줄입니다.** 대화에는 CLI 내장 도구 중 Read·Glob·Grep·TodoWrite·WebSearch·WebFetch·
  ToolSearch, 그리고 자기 기억(CLI 의 MEMORY.md)을 쓰는 Write·Edit 만 실립니다(`JARVIS_BUILTIN_TOOLS`).
  기억 폴더는 카드 없이 읽고 씁니다(쓰기는 CLI 가 자동 기억 폴더만 따로 허용합니다. 다른 곳의 쓰기는 카드).
  그래서 대화 중 읽은 페이지가 기억에 지시를 심으면 이후 세션에도 실립니다. 카드 대신 기억이 바뀔 때마다
  바뀐 글을 담은 알림("자비스가 새로 기억했어요", 알림 설정의 "자비스 기억")이 갑니다. 주문 조회와 작업은 `sonnet`, 잘 안 끝나 다시
  하는 작업만 `opus` 로 돌고(`JARVIS_ENRICH_MODEL`, `JARVIS_JOBS_MODEL`, `JARVIS_JOBS_RETRY_MODEL`),
  대화 턴이 돌고 있지 않으면 바로 시작합니다(대화가 끝나고 5분 기다리던 규칙은 2026-10-18 에 뺐습니다 -
  방금 맡긴 작업이 '예약됨'으로 5분씩 멈춰 있었습니다). 대화 때문에 끊긴 실행은 양보 백오프로 다시 잡힙니다.
- **기록합니다.** 에이전트 로그의 `턴 사용량`·`백그라운드 사용량` 줄에 요청 수, 입력·캐시 읽기·
  캐시 쓰기(5분/1시간)·출력 토큰, 마지막 요청의 크기(`context`), 고정부와 기록 추정치,
  직전 턴과의 간격이 남습니다. 위 기준값은 이 숫자로 정합니다.
- CLI 버전은 `agent/Dockerfile` 에 고정돼 있습니다. 올릴 때는 위 로그를 전후로 비교합니다.

## 개발 중 따로 띄우기

에이전트는 혼자 뜨지 못합니다. 부팅할 때 `calendar-svc` 와 `chat-svc` 의
`/health` 를 확인하고, 없으면 포기합니다 — 빈 대화로 시작해서 첫 턴에 기록을
덮어쓰는 것보다 뜨지 않는 편이 낫습니다. 그래서 의존 서비스는 도커로 띄운 채
에이전트만 로컬에서 돌리는 것이 가장 편합니다.

```bash
# 데이터베이스와 기능 서비스만 도커로
docker compose up -d postgres calendar chat workout assets

# 터미널 1 — 에이전트
cd agent && CLAUDE_CODE_OAUTH_TOKEN=... JARVIS_WORKSPACE=../workspace \
  JARVIS_CALENDAR_URL=http://localhost:8093 \
  JARVIS_CHAT_URL=http://localhost:8094 \
  JARVIS_API_TOKEN=<.env 의 값> \
  JARVIS_DEBUG=true go run ./cmd/agent

# 터미널 2 — 웹 (게이트웨이 없이 서비스에 바로 붙으므로, 게이트웨이 토큰 자리에 서비스 토큰을 넣는다)
cd web && AGENT_URL=http://localhost:8080 \
  WORKOUT_URL=http://localhost:8091 \
  ASSETS_URL=http://localhost:8092 \
  JARVIS_EDGE_TOKEN=<.env 의 JARVIS_API_TOKEN 값> npm run dev
```

토큰을 빼먹으면 서비스들이 401을 돌려줍니다. 서비스 하나만 고칠 때는 그것만
로컬에서 돌리고 나머지는 도커에 두면 됩니다 — 각 서비스의 README 에 단독 실행
명령이 있습니다.

로컬에 `claude` CLI가 없으면 `JARVIS_CLAUDE_BIN`으로 경로를 지정하세요.

## 에이전트 API

| 메서드 | 경로 | 하는 일 |
|---|---|---|
| GET | `/thread` | 스레드 + 진행 여부 + 제안 전체 (최초 로딩) |
| GET | `/events` | SSE. `turn` / `status` / `error` / `proposal` / `calendar` |
| POST | `/messages` | `{"text":"..."}` → 202, 결과는 SSE로 |
| GET | `/proposals` | 제안 전체 |
| POST | `/proposals/{id}/decision` | `{"decision":"approved"\|"rejected"}` |
| GET | `/usage` | 구독 사용량 |
| POST | `/uploads` · GET `/uploads/{name}` | 사진 첨부 |
| GET | `/calendar?from=&to=` | 기간 조회 (양끝 포함) |
| POST | `/calendar` | 사람이 직접 추가·수정. id 없으면 추가 |
| DELETE | `/calendar/{id}` | 사람이 직접 삭제 |
| POST | `/consume` | events-svc 가 mind 카드를 배달하는 곳 |
| GET·POST | `/browser-checks` | 브라우저 로그인 확인 결과 / 다시 확인 요청 (status-svc 가 읽음) |
| POST | `/intercept` | 브라우저 결제 가드가 묻는 곳 (가드 전용 토큰) |
| POST | `/mcp` | Claude Code 전용 권한 질의 (사람이 쓰는 곳 아님) |
| POST | `/mcp/<모듈>` | Claude Code 전용 모듈 도구 (사람이 쓰는 곳 아님) |
| GET | `/healthz` | 헬스체크 |

`turn`과 `proposal` 이벤트는 id 기준 upsert입니다. 결재된 카드는 같은 proposal id로
다시 옵니다. 느린 구독자는 프레임을 잃을 수 있으므로, 모든 화면은 재연결 때 위의
조회 API로 다시 채웁니다.

사람이 캘린더에 직접 쓰는 경로는 결재를 거치지 않습니다. 자기가 친 것을 자기가
승인하는 건 연극입니다.

## 검증 상태

Docker로 스택을 올려 확인한 것 (Claude Code 2.1.278):

- **승인 게이트** — MCP `tools/call`이 결정 전까지 반환하지 않음. 승인 시
  `{"behavior":"allow"}`, 반려 시 `{"behavior":"deny","message":...}`.
- **MCP 연결** — 실제 컨테이너 안에서 CLI가 `mcp_servers: [{status:"connected"}]`.
- **CLI 플래그** — 사용하는 플래그 전부 유효. 단 `--permission-prompt-tool`은
  `--help`에 정의가 없는 **숨겨진 플래그**다(다른 항목 설명에서만 언급됨).
  가짜 플래그는 `unknown option`으로 거부되는데 이건 통과하므로 살아 있다.
- **`--permission-mode manual`** — init 이벤트에 `default`로 보고된다. 다른 모드는
  전부 그대로 보고되므로 `manual`은 `default`(전부 물어봄)의 별칭이다. 게이트 유지됨.
- **stream-json 파싱** — `system/init`의 `session_id`,
  `assistant.message.content[].text`, `result.is_error`를 실제 출력으로 확인.
- **이미지** — agent 417MB(Node + CLI 포함). 웹은 이미지가 아니라 Cloudflare Worker 다.

제안/캘린더 분리 후 HTTP로 직접 확인한 것 (CLI 없이 `go run`):

- **게이트가 여전히 막는다** — `/mcp`의 `request_approval`이 결정 전까지 반환하지
  않고, `/proposals/{id}/decision` 승인 뒤에야 `{"behavior":"allow"}`를 돌려준다.
  두 번째 결재는 409.
- **카드를 모듈이 그린다** — `mcp__calendar__create_event` 권한 질의가
  `calendar.create_event`로 매핑되고, 카드 본문이 JSON이 아니라
  `10월 3일 (토) 09:00  캠핑 · 양평`으로 나온다.
- **도구 스키마** — `/mcp/calendar`의 `tools/list`가 required를 정확히 보고한다
  (`create_event`: date·title, `update_event`/`delete_event`: id, `list_events`: 없음).
- **거부되는 입력** — `{"date":"내일"}`은 `isError`로 되돌아온다.
- **SSE** — 캘린더가 바뀌면 `{"type":"calendar",...}` 프레임이 즉시 나간다.

아직 확인 못 한 것:

- **권한 페이로드의 실제 형태.** 스키마가 문서화돼 있지 않아 `tool_name` /
  `toolName` / `tool` / `name` 등을 관용적으로 읽는다. 실제로 Claude가 도구를
  쓰려는 순간을 봐야 확정된다. 카드 제목이 "도구"로만 나오면 파싱이 빗나간 것이니
  `JARVIS_DEBUG=true` 로그의 `permission request` 항목을 보고 고칠 것.
  (디버그와 무관하게 페이로드는 항상 로그에 남긴다.)
- **대화 품질** — 실제 토큰으로 한 턴도 돌려본 적 없음.
- **모델이 캘린더 도구를 제대로 쓰는지** — 도구 자체는 HTTP로 검증했지만, 실제 턴에서
  Claude가 `list_events`로 id를 먼저 확인하고 상대 날짜를 변환하는지는 못 봤다.

## 설계 근거

UI 쪽 팔레트·타이포·레이아웃 근거는 [web/DESIGN.md](web/DESIGN.md)에 있습니다.
