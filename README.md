# jarvis

개인 AI 어시스턴트. 1:1 스레드 하나에서 대화하고, 상태를 바꾸는 일은 실행 전에 승인을 받습니다.
**Claude Code 구독으로 과금됩니다** (API 크레딧 아님).

```
web (Next.js)  ──┐  사람이 읽고 결재하는 화면 (대화 / 캘린더)
                 ├─ /api/agent/* 프록시 (자격증명은 서버에만)
agent (Go)     ──┤
                 ├─ core/proposal        ← 제안과 결재. 이 시스템의 심장
                 ├─ MCP /mcp             ← 권한 질의를 제안으로 바꾸는 어댑터
                 ├─ MCP /mcp/calendar    ← 모듈이 모델에게 내주는 도구
                 └─ exec: claude -p ...  ← 구독으로 과금, 실제 실행
```

제안(proposal)은 저장되는 객체이지 대화의 속성이 아닙니다. 그래서 **대화 없이도
제안이 열릴 수 있습니다** — 나중에 카톡 감지기가 "캠핑 일정 추가할까요?"를 띄우는
자리가 여기입니다. 지금은 권한 게이트가 유일한 생산자입니다.

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

- 저장 위치는 `JARVIS_CALENDAR_PATH`(컨테이너에선 `/data/calendar.json`)이며,
  **워크스페이스 바깥**입니다. 안에 두면 CLI의 `Read`/`Write` 도구가 모듈의 스키마와
  승인 카드를 우회하는 두 번째 문이 됩니다.
- 모델에겐 도구 4개만 보입니다: `list_events`(무승인) / `create_event` /
  `update_event` / `delete_event`.
- 시간대는 `TZ`(기본 `Asia/Seoul`)입니다. 컨테이너 기본값인 UTC로 두면 한국 시간
  자정~09시 사이에 "오늘"이 하루 밀립니다. zoneinfo는 바이너리에 내장돼 있어
  이미지에 `tzdata`를 깔 필요가 없습니다.
- 날짜는 `YYYY-MM-DD`, 시각은 `HH:MM`만 받습니다. "다음 주 화요일"은 저장 단계에서
  거부되고, 변환은 모델이 합니다 — 오늘 날짜는 **매 턴** 시스템 프롬프트에 주입됩니다
  (부팅 시 한 번이 아니라).

## 실행

```bash
npx @anthropic-ai/claude-code setup-token    # 1년짜리 토큰 발급
cp .env.example .env                          # CLAUDE_CODE_OAUTH_TOKEN 채우기
docker compose up --build
# http://localhost:3000
```

`ANTHROPIC_API_KEY`가 환경에 있으면 **에이전트가 부팅을 거부합니다.** 조용히 API
종량 과금으로 새는 것을 막기 위한 의도적인 동작입니다.

## 개발 중 따로 띄우기

```bash
# 터미널 1
cd agent && CLAUDE_CODE_OAUTH_TOKEN=... JARVIS_WORKSPACE=../workspace \
  JARVIS_DEBUG=true go run ./cmd/agent

# 터미널 2
cd web && AGENT_URL=http://localhost:8080 npm run dev
```

로컬에 `claude` CLI가 없으면 `JARVIS_CLAUDE_BIN`으로 경로를 지정하세요.

## 에이전트 API

| 메서드 | 경로 | 하는 일 |
|---|---|---|
| GET | `/thread` | 스레드 + 진행 여부 + 제안 전체 (최초 로딩) |
| GET | `/events` | SSE. `turn` / `status` / `error` / `proposal` / `calendar` |
| POST | `/messages` | `{"text":"..."}` → 202, 결과는 SSE로 |
| GET | `/proposals` | 제안 전체 |
| POST | `/proposals/{id}/decision` | `{"decision":"approved"\|"rejected"}` |
| GET | `/calendar?from=&to=` | 기간 조회 (양끝 포함) |
| POST | `/calendar` | 사람이 직접 추가·수정. id 없으면 추가 |
| DELETE | `/calendar/{id}` | 사람이 직접 삭제 |
| POST | `/mcp` | Claude Code 전용 권한 질의 (사람이 쓰는 곳 아님) |
| POST | `/mcp/calendar` | Claude Code 전용 캘린더 도구 (사람이 쓰는 곳 아님) |
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
- **이미지** — agent 417MB(Node + CLI 포함), web 229MB.

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
