/**
 * The findings of the 2026-10-17 code review, as data. The screen
 * (report.tsx) draws them in the 상태 tab's row grammar; nothing here is
 * fetched - the report is a snapshot of one reading of the repository, and
 * it says so at the bottom (BASIS).
 *
 * Backticks in a string are drawn as code (a file, a command).
 */

export type Priority = "now" | "next" | "later";

export type Finding = {
  id: string;
  group: string;
  priority: Priority;
  name: string;
  /** One line under the name: what is wrong and why it matters. */
  summary: string;
  /** What was seen, with where. */
  evidence: string[];
  /** What to do, concretely. */
  fix: string;
  /** How much work the fix is. */
  effort: "작음" | "보통" | "큼";
};

export const PRIORITY: Record<Priority, { label: string; meaning: string }> = {
  now: { label: "지금", meaning: "이번 주에 손대는 것이 맞아요" },
  next: { label: "다음", meaning: "다음 작업 묶음에 넣어요" },
  later: { label: "나중", meaning: "알고만 있다가 때가 오면" },
};

export const GROUPS = ["보안·경계", "신뢰성·데이터", "품질·테스트", "운영·의존성"];

export const FINDINGS: Finding[] = [
  // ── 보안·경계 ───────────────────────────────────────────────────────
  {
    id: "next-advisories",
    group: "보안·경계",
    priority: "now",
    name: "Next.js 보안 권고 (critical 1 · high 9)",
    summary: "콘솔이 쓰는 next 16.3.5 에 캐시 오염·SSRF 권고가 있어요. 16.4.2 로 올리면 닫혀요.",
    evidence: [
      "`cd web && npm audit --omit=dev`: 10건(critical 1, high 9). next 16.3.5 - SSG/ISR 캐시 오염으로 다른 사용자의 콘텐츠가 치환되거나 지속적 DoS(GHSA-mcj8-r9mp-w47p), 이미지 최적화의 SSRF(GHSA-cjq9-62q9-8jv4).",
      "sharp <0.35.5(librsvg CVE-2026-96889), source-map-js 1.0.0~1.2.1(이벤트 루프 DoS) - 둘 다 `npm audit fix` 로 닫혀요.",
      "완화 요인: 콘솔은 Cloudflare Access 뒤에 있고 ISR/SSG 캐시를 쓰지 않아요(`web/open-next.config.ts` 에 캐시 없음). 실제 노출면은 좁지만, 공개 저장소의 콘솔이 알려진 취약 버전을 들고 있는 것은 남아요.",
    ],
    fix: "`cd web && npm i next@16.4.2 eslint-config-next@16.4.2 && npm audit fix` 뒤 `npm run cf:build` 로 확인해요. 마이너 업그레이드라 `web/AGENTS.md` 가 가리키는 변경 안내를 한 번 읽고 올려요.",
    effort: "작음",
  },
  {
    id: "edge-untested",
    group: "보안·경계",
    priority: "next",
    name: "게이트웨이(jarvis-be)에 테스트가 없다",
    summary: "운영자·손님 토큰, VNC 입장권, 에이전트 경로 허용 목록을 231줄 하나가 가르는데 자동 검증이 없어요.",
    evidence: [
      "`edge/src/index.ts` 231줄: 두 문(Internal·공개), 손님 서비스 집합, `AGENT_ROUTES`, `ticketValid` 의 HMAC, 응답의 `X-Jarvis-Agent` 대조 - 전부 보안 판단이에요.",
      "최근 60일 변경 17회(변경 횟수 상위 파일). 2026-10-17 의 S-1·S-2 재조치도 여기였고, 그때 '커밋 있음' 대조만으로 끝났다가 실제로는 안 고쳐진 항목이 뒤늦게 발견됐어요.",
      "`edge/` 에 테스트 파일도 테스트 러너도 없어요.",
    ],
    fix: "판단 함수(`ticketValid`, 경로·토큰 → 허용/거부)를 `edge/src/policy.ts` 로 빼고 vitest 로 표 형태 테스트를 둬요: 공개 문에서 콘솔 토큰은 404, 손님 토큰으로 `/assets` 는 404, 만료 입장권은 401, `/agent/mcp` 는 404, 다른 agent 의 응답은 502. `wrangler dev` 통합 테스트는 그 다음이에요.",
    effort: "보통",
  },
  {
    id: "session-cookie",
    group: "보안·경계",
    priority: "later",
    name: "콘솔 세션 쿠키를 무효화할 수 없다",
    summary: "쿠키가 '만료.서명' 뿐이라 로그아웃해도 복사본은 2주간 유효해요. Access 뒤에서는 낮은 위험이에요.",
    evidence: [
      "`web/src/lib/auth.ts` 의 `issue()` 가 만료 시각만 서명해요 - 같은 초에 발급한 두 세션은 같은 값이고, 주체도 발급 id 도 없어요.",
      "`/api/auth/logout` 은 브라우저의 쿠키를 지울 뿐이에요. 서버에서 끊을 수단은 서명 키 `JARVIS_AUTH_SECRET` 교체뿐이에요.",
      "완화: 운영 콘솔은 Cloudflare Access 의 JWT 가 세션이고(`web/src/middleware.ts`), 비밀번호 경로는 로컬·LAN 용이에요.",
    ],
    fix: "쿠키에 발급 id(랜덤 16바이트)와 발급 시각을 넣고, Worker 비밀값에 '이 시각 이전 발급은 무효' 하나를 두면 로그아웃·분실 때 전부 끊을 수 있어요. 비밀번호 경로를 아예 끄는 것도 선택지예요.",
    effort: "작음",
  },
  {
    id: "chromium-sandbox",
    group: "보안·경계",
    priority: "later",
    name: "에이전트 브라우저의 Chromium 샌드박스가 꺼져 있다 (받아들인 위험)",
    summary: "페이지가 렌더러를 장악하면 browser 사용자(uid 10002)의 권한이 전부예요. 컨테이너·네트워크 격리가 그 뒤를 받치고 있어요.",
    evidence: [
      "`browser/Dockerfile`: browser-use 가 `--no-sandbox` 로 띄워요(컨테이너에 사용자 네임스페이스가 없어 'No usable sandbox', 2026-10-17 확인).",
      "보완은 돼 있어요: 비root(10002), `browser` 네트워크에만 연결, 결제 가드(`browser/guard.py`)가 CDP 에서 요청을 막고, vault 는 꺼내기 토큰만 줘요.",
      "`browser/requirements.txt` 의 `uvicorn` 은 버전 고정이 없어요(browser-use 는 0.13.10 고정).",
    ],
    fix: "setuid `chrome-sandbox` 도우미를 이미지에 넣고 `CHROME_DEVEL_SANDBOX` 로 켜는 방법을 한 번 시험해요(Docker Desktop 에서 되면 가장 싼 보강). 안 되면 지금 격리를 유지하고 `uvicorn==` 만 고정해요.",
    effort: "보통",
  },

  // ── 신뢰성·데이터 ───────────────────────────────────────────────────
  {
    id: "unbounded-thread",
    group: "신뢰성·데이터",
    priority: "next",
    name: "대화·카드가 끝없이 자라고, 매번 전부 실린다",
    summary: "첫 로딩마다 스레드 전체와 카드 전체를 받아요. 지금은 괜찮지만 달이 쌓일수록 폰의 첫 화면이 느려져요.",
    evidence: [
      "`chat-svc/internal/store/store.go` 의 `All()`: 페이지도 상한도 없어요(주석에 '느려지면 UI 가 창을 요청하는 것이 답' 이라고 적어 뒀어요).",
      "`agent/internal/httpapi/router.go` 의 `getThread`: 턴 전체 + `proposals.List()` 전체. 재연결·탭 복귀마다 다시 받아요(`web/src/lib/use-thread.ts` 의 hydrate).",
      "`agent/internal/thread/store.go` 의 `Find()` 는 참조 답장 한 건을 찾으려고 스레드 전체를 다시 내려받고, `runner.go` 의 `recap()` 도 최근 30턴을 쓰려고 전체를 받아요.",
      "`agent/internal/core/proposal/proposal.go`: 카드 전부를 `proposals.json` 하나에 두고 결정마다 파일 전체를 다시 써요. `suggest.findCard` 는 mind 이벤트마다 모든 카드의 입력을 JSON 으로 풀어 봐요.",
    ],
    fix: "chat-svc 에 `GET /turns?before=&limit=` 와 `GET /turns/{id}` 를 더하고, `/thread` 는 최근 200턴 + 미결·최근 30일 카드만 돌려주며 화면은 위로 당기면 더 받아요. 결정된 카드는 30일 뒤 아카이브 파일(또는 비워 둔 `JARVIS_DATABASE_URL`)로 옮겨요. 상태 탭에 스레드 길이를 한 줄로 두면 언제 손댈지 보여요.",
    effort: "보통",
  },
  {
    id: "retention",
    group: "신뢰성·데이터",
    priority: "next",
    name: "보존 기한이 표마다 다르거나 없다",
    summary: "이벤트 90일·위치 30일·메시지 730일은 정해져 있는데 대화 턴·결정된 카드·알림함·자산 스냅샷은 무기한이에요.",
    evidence: [
      "정해진 것: events 90일(`events-svc/main.go`), `context_raw` 30일, 카톡·iMessage 730일(`gocommon/collect`), mind 메시지 365일, 작업 사진 720시간.",
      "없는 것: `turns`(chat-svc), `proposals.json`, 알림함(notify-svc 는 실패한 푸시 구독만 지워요), `card_transactions`, `portfolio_snapshots`.",
      "백업 보존은 14일(`scripts/backup`)이라 지운 뒤의 복구 창도 짧아요 - 기한을 정할 때 같이 봐야 해요.",
    ],
    fix: "`db/README.md` 에 표별 보존 기한 표를 하나 두고, 무기한으로 둘 것(자산 스냅샷·결제 내역)은 '의도' 라고 적어요. 알림함은 90일, 카드는 위 항목의 아카이브로.",
    effort: "작음",
  },
  {
    id: "log-cap",
    group: "신뢰성·데이터",
    priority: "next",
    name: "컨테이너 로그에 상한이 없다",
    summary: "compose 에 logging 설정이 없어 맥미니 Docker 의 기본값(json-file, 무제한)이면 로그가 디스크를 채울 수 있어요.",
    evidence: [
      "`docker-compose.yml` 의 서비스 23개 중 `logging:` 블록이 0개예요.",
      "에이전트는 턴마다 `permission request` 페이로드와 사용량 줄을 JSON 으로 남기고, 디버그를 켜면 스트림 이벤트까지 찍어요.",
      "맥미니의 `daemon.json` 에 `log-opts` 가 있는지는 이번 분석에서 확인하지 못했어요.",
    ],
    fix: "compose 맨 위에 `x-logging: &log` (json-file, max-size 20m, max-file 5)를 두고 각 서비스에 `logging: *log` 를 붙여요. 맥미니에서 `docker system df` 로 지금 크기를 한 번 봐요.",
    effort: "작음",
  },
  {
    id: "events-skip",
    group: "신뢰성·데이터",
    priority: "later",
    name: "이벤트 배달에서 4xx 는 조용히 건너뛴다",
    summary: "소비자가 일시적인 버그로 400을 돌려주면 그 이벤트는 그 소비자에게 영영 가지 않아요.",
    evidence: [
      "`events-svc/internal/bus/bus.go` 의 `pass()`: 408·429 를 뺀 4xx 는 경고 로그 뒤 커서를 전진해요.",
      "mind 카드(`mind.card`)와 결제 결정이 이 길로 다니므로, 건너뛴 카드는 운영자에게 닿지 않은 채 끝나요.",
    ],
    fix: "건너뛴 이벤트를 `event_deliveries_failed`(소비자·이벤트 id·응답)에 남기고 상태 탭 '기반' 에 '배달 못 한 이벤트 N' 한 줄을 더해요. 재전송은 `PUT /subscriptions/{consumer}` 의 fromStart 로 이미 가능해요.",
    effort: "작음",
  },

  // ── 품질·테스트 ─────────────────────────────────────────────────────
  {
    id: "assets-untested",
    group: "품질·테스트",
    priority: "now",
    name: "주문을 내는 assets-svc 에 테스트가 없다",
    summary: "업비트·한투에 실제 주문을 보내는 4,600줄(TypeScript)에 테스트 파일이 0개예요. 돈이 나가는 코드 중 유일하게 비어 있어요.",
    evidence: [
      "`assets-svc/src`: orders.ts(검증·매도 우선·상한), standing.ts(대기 주문 재제출), kis-flows.ts(입출금 감지), trade-plan.ts(리밸런싱) - 테스트 0개, `package.json` 에 test 스크립트도 없어요.",
      "같은 저장소의 Go 서비스는 테스트 96파일(70패키지 통과), browser 는 `test_guard.py`, mind 는 시나리오 58개가 있어요.",
      "2026-10-17 의 기록: KO 재매수 대기 주문이 의도 확인 전에 취소된 일, 결제 전 체결이 원화 0 으로 보이는 함정 - 회귀 테스트로 굳혀 둘 사례들이에요.",
    ],
    fix: "Node 내장 `node --test` 와 `tsx` 로 시작해요: `problem()` 의 경계값(상한·보유 초과·₩5,000 미만), `placeOrders` 를 가짜 venue 로(매도 먼저, 계획 상한, 한 건의 실패가 나머지를 막지 않음), `standingPass` 의 세션·만료 분기, `rebalancePlan` 의 비중 계산. CI 에 넣어요.",
    effort: "보통",
  },
  {
    id: "no-ci",
    group: "품질·테스트",
    priority: "now",
    name: "CI 가 없다 - 깨진 린트가 이미 들어와 있다",
    summary: "저장소에 워크플로가 없어 테스트·린트·빌드가 사람 손에만 달려 있어요. 지금 npm run lint 는 오류 2개로 실패해요.",
    evidence: [
      "`.github/` 가 없고 Makefile·pre-commit 도 없어요. Go 모듈 16개·web·Python 2개를 전부 손으로 돌려야 해요.",
      "`web/src/lib/use-kakao.ts` 131·138행의 react-hooks/set-state-in-effect 오류 2건이 커밋돼 있어요.",
      "공개 저장소인데 비밀값 검사(gitleaks 등)가 없어요. 2026-10-16 에 이력을 filter-repo 로 지운 일이 있었어요.",
      "DB 테스트(`scripts/dbtest`)와 memory-svc 테스트는 Docker 가 필요한데, CI 의 service 컨테이너로 돌릴 수 있어요.",
    ],
    fix: "GitHub Actions 하나: (1) Go 매트릭스 `go vet && go test ./...` (2) web `tsc --noEmit && eslint src && cf:build` (3) browser `python -m unittest test_guard` (4) gitleaks. Postgres service 컨테이너로 `scripts/dbtest` 는 두 번째 단계예요. 배포는 CI 에 두지 않아요(요청 있을 때만 배포하는 지금 방식 유지).",
    effort: "보통",
  },
  {
    id: "web-lint",
    group: "품질·테스트",
    priority: "next",
    name: "web 린트의 구멍",
    summary: "eslint 가 빌드 산출물(.open-next)까지 훑고, 같은 훅 규칙을 28곳에서 끄고 있어요.",
    evidence: [
      "`web/eslint.config.mjs` 의 ignore 에 `.open-next/**` 가 없어 `npm run lint` 가 9,347건을 보고해요(src 만 보면 2건).",
      "`eslint-disable` 28곳 - 대부분 react-hooks/set-state-in-effect. 데이터 훅마다 같은 패턴(fetch → setState → setInterval)을 손으로 반복해요.",
      "`next dev` 가 `middleware` 파일 규약이 deprecated 라고 경고해요(Next 16 은 `proxy` 로 이름을 바꿨어요). 지금은 돌지만 다음 메이저에서 사라져요.",
      "`next dev` 에서는 미들웨어 matcher 가 `/_next/hmr` 을 거르지 않아 HMR 웹소켓 handshake 가 실패하고, 그 상태로 페이지가 hydrate 되지 않았어요(버튼이 반응하지 않음). 운영 빌드(`next build && next start`)는 정상이라 이 보고서의 화면 검증도 그쪽으로 했어요.",
      "`tsc --noEmit` 은 깨끗해요.",
    ],
    fix: "ignore 에 `.open-next/**` 를 더해요. 폴링 훅을 `usePolled(fetcher, interval)` 하나로 모으고(`use-kakao`·`use-usage`·알림함·상태), 그 안에서만 규칙을 한 번 다루면 28곳이 사라져요. `npx @next/codemod@canary middleware-to-proxy .` 로 이름을 바꾸고 matcher 에 `_next/hmr` 제외를 더하면 로컬 개발도 다시 돼요.",
    effort: "작음",
  },
  {
    id: "mind-two-stores",
    group: "품질·테스트",
    priority: "later",
    name: "mind-svc 저장소가 두 벌이다",
    summary: "Postgres 1,128줄과 메모리 1,021줄이 같은 인터페이스를 각각 구현해, 기능마다 두 번 써야 해요.",
    evidence: [
      "`mind-svc/internal/store/postgres.go` 1,128줄, `memory.go` 1,021줄 - 저장소 전체에서 가장 큰 파일 2위·5위이고, 최근 60일에 각각 15회 바뀌었어요.",
      "메모리 모드는 콘솔 시뮬레이터와 `eval`(시나리오 58개)용이에요. `scripts/dbtest` 가 이미 일회용 Postgres 를 띄워요.",
    ],
    fix: "eval 과 시뮬레이터를 일회용 Postgres 위에서 돌리고 메모리 저장소를 지우거나, 메모리 저장소를 읽기 전용 최소 집합으로 줄여요. 두 구현이 같은 답을 내는지 보는 계약 테스트를 먼저 두면 어느 쪽이든 안전해요.",
    effort: "큼",
  },
  {
    id: "agent-main",
    group: "품질·테스트",
    priority: "later",
    name: "에이전트 main.go 가 배선 511줄로 커졌다",
    summary: "모듈·백그라운드·알림·재로그인 작업이 한 함수에 있고, 60일간 32번 바뀐 가장 손 많이 탄 Go 파일이에요.",
    evidence: [
      "`agent/cmd/agent/main.go` 511줄, 변경 횟수 2위(1위는 `docker-compose.yml` 57회).",
      "`relogin()` 의 백그라운드 작업 프롬프트, memorywatch, 카드→푸시 매핑이 main 에 직접 있어요.",
    ],
    fix: "`cmd/agent/wire_modules.go`·`wire_background.go`·`wire_notify.go` 로 나누고 `relogin` 은 `internal/browsercheck` 로 옮겨요. 동작은 그대로, 파일만 나눠요.",
    effort: "작음",
  },

  // ── 운영·의존성 ─────────────────────────────────────────────────────
  {
    id: "env-example",
    group: "운영·의존성",
    priority: "next",
    name: ".env.example 로는 스택이 뜨지 않는다",
    summary: "compose 가 반드시 요구하는 값 20개가 예제 파일에 없고, 예제에만 있는 키 4개는 이제 Worker 의 비밀값이에요.",
    evidence: [
      "필수(`:?`)인데 `.env.example` 에 없음: `JARVIS_API_TOKEN`, `JARVIS_DB_PASSWORD_*` 15개, `JARVIS_GUEST_API_TOKEN`, `JARVIS_VAULT_KEY`, `JARVIS_VAULT_RELEASE_TOKEN`, `MIND_CONTENT_KEY`.",
      "compose 가 읽는 선택값 95개가 예제에 없어요. 예제의 `JARVIS_AUTH_*`·`JARVIS_WEB_PORT` 는 compose 가 읽지 않아요(웹은 Worker).",
      "예제의 `JARVIS_ALLOWED_TOOLS` 에 `Read,Glob,Grep` 이 있는데 에이전트가 기동 때 떼어 내며 경고해요(`agent/internal/config/config.go` 의 `unsafeTool`).",
      "맥미니 이전·복구 때 README 의 `cp .env.example .env` 절차가 첫 단계에서 멈춰요.",
    ],
    fix: "`scripts/env-example` 로 compose 에서 변수 목록을 뽑아 예제를 만들고(필수는 빈 값 + 만드는 명령 주석), Worker 비밀값은 `DEPLOY.md` 쪽 절로 옮겨요. CI 에서 예제와 compose 의 차이를 검사해요.",
    effort: "작음",
  },
  {
    id: "go-boilerplate",
    group: "운영·의존성",
    priority: "next",
    name: "Go 서비스 11개가 같은 보일러플레이트를 복제한다",
    summary: "베어러 인증·JSON 응답·pgx 풀·종료 처리가 서비스마다 손으로 복사돼 있어요. gocommon 은 있지만 이걸 담지 않아요.",
    evidence: [
      "`subtle.ConstantTimeCompare` 15파일, `func writeJSON` 16파일, `pgxpool.New` 19파일, 서비스마다 `auth()`·`same()`.",
      "`ReadHeaderTimeout` 은 agent·imessage 둘만 설정했어요. 나머지는 `http.Server{}` 기본값(무제한)이에요.",
      "손님 토큰 분기(chat·events·calendar·memory)는 넷이 미묘하게 다른 코드예요.",
    ],
    fix: "`gocommon/httpx`: `Bearer(token, guestToken)` 미들웨어(컨텍스트에 역할), `JSON`/`Err`, `NewServer(addr, h)`(ReadHeaderTimeout 10s·IdleTimeout), `gocommon/pgx.Open(dsn, maxConns)`. 서비스 하나씩 바꿔 끼워요 - 이미 `collect`·`notify`·`site` 를 그렇게 옮긴 전례가 있어요.",
    effort: "보통",
  },
  {
    id: "deps-drift",
    group: "운영·의존성",
    priority: "next",
    name: "의존성 버전이 흩어져 있고 올리는 손이 없다",
    summary: "go 1.24 와 1.25.0 이 섞여 있고 pgx 는 5.7.5 와 5.11.0, x/crypto 는 0.37 까지 있어요. 자동 갱신(Dependabot)이 없어요.",
    evidence: [
      "`go` 지시어: events·mind·notify·phonenote 는 1.24, 나머지는 1.25.0. 로컬 Go 는 1.24.3(툴체인 자동 내려받기로 돌아요).",
      "`mind-svc/go.mod`: pgx v5.7.5, x/crypto v0.37.0, x/net v0.21.0 vs agent 의 pgx v5.11.0. `go list -m -u` 기준 mind 13개·agent 16개가 뒤처져 있어요.",
      "web: next·react·wrangler·lucide 등 12개 outdated. memory-svc 는 반대로 모범이에요(`constraints.txt` 로 전부 고정 + `patches.py` 가 버전 불일치면 기동 거부).",
      "`go.work` 가 없어 gocommon 을 바꾸면 모듈 8개에서 각각 확인해야 해요.",
    ],
    fix: "`go.work` 를 추가하고(use ./agent ./gocommon …), 모듈 전부 `go 1.25` 로 통일한 뒤 `go get -u ./... && go mod tidy`. Dependabot(gomod·npm·pip·docker) 주 1회. 올린 뒤 `scripts/dbtest` 전체를 돌려요.",
    effort: "작음",
  },
  {
    id: "docs-lag",
    group: "운영·의존성",
    priority: "later",
    name: "문서가 코드보다 늦다",
    summary: "README 의 '검증 상태' 는 운영 전 시점 그대로이고, web/README 는 create-next-app 기본 문서예요.",
    evidence: [
      "`README.md` 검증 상태: '대화 품질 - 실제 토큰으로 한 턴도 돌려본 적 없음', '권한 페이로드의 실제 형태 아직 확인 못 함' - 운영 4주째와 맞지 않아요.",
      "`web/README.md` 는 생성 도구의 기본 문서예요. 루트의 `README.sample.md` 는 추적되지 않은 잔여 파일이에요.",
      "반대로 `DEPLOY.md`·`db/README.md`·서비스별 README 는 잘 살아 있어요.",
    ],
    fix: "README 의 검증 상태 절을 '운영에서 확인된 것 / 아직 모르는 것' 으로 다시 쓰고, web/README 는 지우거나 `DESIGN.md` 를 가리키는 세 줄로 줄여요. `README.sample.md` 는 정리해요.",
    effort: "작음",
  },
];

/** What already holds up well: the reader wrote this system, and knowing what not to touch is half of prioritising. */
export const STRENGTHS: { name: string; summary: string }[] = [
  {
    name: "결재 게이트",
    summary: "제안이 대화와 분리된 저장 객체라 대화 없이도 카드를 열 수 있고, 되돌릴 수 없는 동작은 신뢰도와 무관하게 카드를 거쳐요.",
  },
  {
    name: "토큰 분리",
    summary: "콘솔 edge 토큰 → 게이트웨이가 서비스 토큰으로 바꿔 붙이고, 손님 토큰은 손님 것만 열어요. 비교는 전부 상수 시간이에요.",
  },
  {
    name: "격리",
    summary: "서비스별 DB 역할, 네트워크 셋(default·data·browser), 비root 이미지, 봉인된 vault 와 mind 본문.",
  },
  {
    name: "데이터 안전",
    summary: "이벤트 outbox, 한 트랜잭션으로 적용·기록되는 마이그레이션, 매일 백업과 복원 시험.",
  },
  {
    name: "테스트 문화",
    summary: "Go 70패키지 전부 통과·vet 깨끗, mind 시나리오 58개. 빈 곳은 assets-svc 와 edge 둘뿐이에요.",
  },
];

/** What this report is a reading of, and what it did not look at. */
export const BASIS = {
  commit: "bf138cd",
  date: "2026-10-17",
  facts: [
    "기준: main `bf138cd` (2026-10-17). 2026-09-19 첫 커밋 이후 203 커밋, 소스 약 7만 8천 줄(Go·TypeScript·Python·SQL).",
    "돌린 것: `go vet`·`go test ./...` 16모듈(70패키지 통과), `tsc --noEmit`(깨끗), `eslint`(오류 2), `npm audit`, `go list -m -u`, compose 와 .env.example 대조, 전 서비스 소스 읽기.",
    "못 한 것: Python 테스트(맥북에 pytest 없음, `scripts/dbtest` 의 Docker 경로는 안 돌렸어요), 맥미니의 운영 상태(디스크·로그 크기·실제 스레드 길이).",
  ],
};
