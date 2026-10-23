# Cloudflare 배포

## 무엇이 어디에 올라가는가 (2026-10-07부터)

| 조각 | 어디서 도는가 | 주소 |
| --- | --- | --- |
| `web` (Next.js 콘솔) | Cloudflare Workers | `jarvis.choigonyok.com` |
| `edge` (게이트웨이) | Cloudflare Workers | `jarvis-be.choigonyok.com` |
| 나머지 전부 (agent, browser, 각 서비스, postgres) | 맥미니의 docker compose | 공개 주소 없음 |

```
브라우저 ─▶ jarvis.choigonyok.com (web Worker, 로그인)
              │ 서비스 바인딩 GATEWAY → 엔트리포인트 Internal
              │ + Bearer JARVIS_EDGE_TOKEN (운영자) / JARVIS_EDGE_GUEST_TOKEN (손님)
              ▼
           jarvis-be (edge Worker)
              │ 토큰 없으면 401, 손님 토큰은 agent-guest·workout·notify·tts 와 context 의 GET track·owntracks/setup/guest,
              │ POST owntracks/regions/guest, 손님 자기 출발 경로(transit/guest…·transit/lines)와
              │ jobs 의 작업 탭 경로(목록·하나·지시·실행·멈춤·다시·그만, 손님 토큰으로 - 손님 작업만)만,
              │ agent 는 콘솔이 쓰는 경로만
              │ 확인 뒤 Authorization 을 JARVIS_API_TOKEN 으로 바꿔 붙인다
              │ (agent-guest 에는 JARVIS_GUEST_API_TOKEN)
              │ Workers VPC 바인딩 (jarvis-agent 터널)
              ▼
           맥미니 compose: agent:8080 · workout:8091 · assets:8092
                           spending:8095 · kakaotalk:8090 · status:8096

폰·맥 수집기 ─▶ jarvis-be.choigonyok.com (공개 주소: 기본 엔트리포인트)
              POST /context/owntracks · /context/signals 와
              입장권 붙은 /vnc/websockify 만. 그 밖은 토큰이 있어도 404.
```

맥미니에서 인터넷을 향해 열린 포트는 없다. 터널(`tunnel` 서비스, `--profile cloudflare`)은
바깥으로 나가는 연결만 맺고, 공개 ingress 도 두지 않는다. Workers VPC 서비스가 그 터널을
타고 compose 이름(`agent` 등)으로 각 서비스에 닿는다.

- VPC 서비스: `npx wrangler vpc service list` (jarvis-agent … jarvis-status, 터널 `5b824fd4…`)
- 게이트웨이 배포: `cd web && npx wrangler deploy -c ../edge/wrangler.jsonc`
- 게이트웨이 비밀: `JARVIS_API_TOKEN`(서비스가 확인하는 토큰, 게이트웨이에만),
  `JARVIS_EDGE_TOKEN`(콘솔이 운영자 요청에 내는 토큰, 콘솔과 게이트웨이 둘 다, 맥북 `~/.config/jarvis/edge-token`),
  `JARVIS_EDGE_GUEST_TOKEN`(콘솔이 손님 요청에 내는 토큰, 둘 다, 맥북 `~/.config/jarvis/edge-guest-token`),
  `JARVIS_GUEST_API_TOKEN`(손님 agent 의 토큰, 게이트웨이와 맥미니 .env).
  `npx wrangler secret put … -c ../edge/wrangler.jsonc`. 콘솔 Worker 에는 `JARVIS_API_TOKEN` 을 두지 않는다.
- 생활 탭(mind): VPC 서비스 `jarvis-mind`(호스트 `mind`, 포트 8102, 같은 터널, 2026-10-19 생성)가
  `edge/wrangler.jsonc` 의 `MIND` 바인딩이다. 게이트웨이는 mind 의 읽기 넷(노트·지도·물건·식사)과
  노트 결정(`POST /mind/api/notes/{id}`)만 연다(`mindRoute`). 콘솔 변수 `MIND_URL`.
- 콘솔 비밀 `JARVIS_VNC_PASSWORD`: 화면 탭이 원격 화면에 낼 비밀번호(맥미니 .env 의 같은 이름과 같은 값, 8자).
- 게이트웨이는 콘솔보다 먼저 배포한다. 공개 주소는 폰의 문만 열려 있어서, 콘솔이 옛 바인딩(기본
  엔트리포인트)으로 부르는 동안 새 게이트웨이가 올라가면 콘솔이 404 를 받는다. 바인딩을 바꿀 때는
  기본 엔트리포인트도 Internal 처럼 도는 과도기 게이트웨이 → 콘솔 → 최종 게이트웨이 순서로.
- 맥북의 `next dev` 는 서비스 바인딩이 없어 공개 주소로 부르는데, 공개 주소는 콘솔 경로를 열지
  않는다 - 로컬 개발은 로컬 compose(`AGENT_URL=http://localhost:8080` 등)로 한다.
- 콘솔 배포: `cd web && npm run cf:build && npm run cf:deploy`. 백엔드 주소는
  `web/wrangler.jsonc` 의 `vars`(AGENT_URL 만 비밀).
- 편집 탭의 PhotoCraft(브라우저에서만 도는 이미지 편집기)는 콘솔의 정적 파일로 같이 올라간다. 빌드 전
  `prebuild` 가 `web/scripts/photocraft.mjs` 로 버전·SHA-256 을 고정한 릴리스를 `web/public/photocraft/`
  (깃 밖)에 받는다. 버전을 올릴 때는 그 스크립트의 버전과 해시(릴리스의 SHA256SUMS.txt)만 바꾼다.
- 맥미니 쪽: `scripts/mini up`, `scripts/mini compose --profile cloudflare up -d tunnel`.
  웹은 컨테이너가 없다(Cloudflare 에서만 돈다).
- 목소리 TTS(`tts/`)는 compose 밖, 맥미니 호스트의 LaunchAgent 다: `scripts/tts install`. 게이트웨이는
  VPC 서비스 `jarvis-tts`(호스트 `host.docker.internal`, 포트 8106, 같은 터널)로 닿는다 - 터널 컨테이너가
  Docker 호스트 주소로 푸는 유일한 경로. 콘솔 변수 `TTS_URL`. 서버는 `~/jarvis/.env` 의
  `JARVIS_API_TOKEN`(운영자 목소리)과 `JARVIS_GUEST_API_TOKEN`(손님 목소리)을 읽는다 - 손님 토큰이
  없으면 손님 목소리만 꺼진다.

**`jarvis.choigonyok.com` 앞에는 Cloudflare Access 가 있다.** Zero Trust 앱 `jarvis`
(팀 `choigonyok.cloudflareaccess.com`, Free 플랜), 정책 `only me` 는 운영자 이메일 하나만 허용하고
로그인은 Google(OAuth 클라이언트는 운영자의 Google Cloud 프로젝트 jarvis-access, 테스트 사용자 본인만)과 예비로 이메일 일회용 코드. 앱·팀 세션 모두 730h(약 1개월). 그 뒤에 콘솔 자체 로그인이 한 번 더 있다.
게이트웨이(`jarvis-be`)는 Access 대상이 아니다 - 사람이 아니라 콘솔 Worker 가 서비스 바인딩으로
부르는 곳이고(공개 주소로는 서비스에 닿지 않는다), 그 안에서 `JARVIS_EDGE_TOKEN`·`JARVIS_EDGE_GUEST_TOKEN` 이
누가 무엇을 여는지 가른다.

## 맥미니의 전제 (배포 전에 갖춰 둔다)

- **FileVault 가 켜져 있어야 한다.** 카톡·iMessage 수집기의 SQLite, 미러(`~/.jarvis-mirror`),
  백업은 평문 사본이다. 디스크 암호화가 그 사본들의 유일한 보호다. `fdesetup status` 로 확인하고,
  꺼져 있으면 시스템 설정 → 개인정보 보호 및 보안 → FileVault 에서 켠다(복구 키는 맥미니 밖에 보관).
  `scripts/backup run` 이 꺼져 있으면 로그에 경고를 남긴다.
  **현재 결정(2026-10-16): 끈 채로 둔다.** 이 맥미니는 정전 뒤 사람 없이 다시 떠야 하고, FileVault 를
  켜면 재부팅 때 맥미니 앞에서 암호를 넣기 전까지 서비스가 뜨지 않는다. 받아들인 위험으로 맥미니 `.env`
  의 `JARVIS_FILEVAULT_OFF_ACCEPTED=true` 에 적혀 있고, 상태 탭은 이를 경고 대신 결정으로 보여 준다.
  대신 평문 사본에는 보존 기한이 있다(메시지 365·730일, 백업 14일). 켜기로 바꾸면 그 줄을 지운다.
- **Time Machine 대상 디스크가 있어야 한다.** `scripts/backup` 은 매일 `~/jarvis-backups` 에
  떠 두기만 하고, 맥미니 밖으로 옮기는 것은 Time Machine 이다. `tmutil destinationinfo` 로 확인한다.
- **iMessage 미러는 전용 바이너리에만 전체 디스크 접근 권한을 준다.**
  `scripts/imessage-mirror install` 로 바이너리를 LaunchAgent 에 올린다(권한이 없는 동안은 예전 SSH
  방식으로 대신 돌고 5분마다 다시 확인한다). 시스템 설정 → 개인정보 보호 및 보안 → 전체 디스크 접근
  권한에 `~/.local/bin/jarvis-imessage-mirror` 를 추가하면 5분 안에 저절로 직접 복사로 바뀐다. 그 뒤
  원격 로그인(sshd-keygen-wrapper)의 권한을 끄고 authorized_keys 의 jarvis-imessage-mirror 줄을 지운다.
- **카톡 자동 로그인은 전용 바이너리에만 손쉬운 사용 권한을 준다.** 카톡은 인터넷이 오래 끊겼다 돌아오면
  로그아웃되고, 그동안 카톡 수집이 조용히 멈춘다. `scripts/kakaotalk-relogin install`(맥북에서)이 LaunchAgent 로
  올리고, 처음 뜰 때 macOS 가 묻는 손쉬운 사용을 시스템 설정 → 개인정보 보호 및 보안 → 손쉬운 사용에서
  "Jarvis 카톡 자동 로그인"(`/Applications/JarvisKakaoRelogin.app`)에 켠다. 맥북의 Apple Development 인증서로 서명해
  다시 설치해도 허락이 남는다. 비밀번호는 `scripts/kakaotalk-relogin password` 로 맥미니 키체인에 넣는다
  (사람이 직접 입력). 상태 탭의 "카톡 로그인"이 결과를 보여 준다.
- **서비스별 DB 역할 비밀번호:** `scripts/db-roles` (`.env` 의 `JARVIS_DB_PASSWORD_*`).

**root 로 돌던 이미지에서 올릴 때 한 번(2026-10-16):** 수집기와 브라우저가 이제 root 가 아니다.
예전 볼륨의 소유자를 바꾼다 - 새로 만드는 볼륨은 이미지가 처음부터 맞는 소유자로 만든다.

```bash
docker run --rm -v jarvis_jarvis-kakaotalk-data:/d alpine:3.22 chown -R 65532:65532 /d
docker run --rm -v jarvis_jarvis-imessage-data:/d alpine:3.22 chown -R 65532:65532 /d
docker run --rm -v jarvis_chrome-profile:/p alpine:3.22 chown -R 10002:10002 /p
```

브라우저 컨테이너를 다시 띄우면 쿠팡 로그인이 풀린다(네이버는 '로그인 상태 유지'로 남는다).
상태 탭의 "쿠팡 주문 내역"이 이상으로 바뀌면 화면 탭에서 쿠팡에 다시 로그인한다.

위 전제는 `scripts/host-check`(LaunchAgent com.jarvis.host-check, 10분마다)가 점검해
`~/.jarvis-host/host.json` 에 적고, 상태 탭 "기반"의 맥미니 디스크 암호화 · 백업의 바깥 사본 ·
iMessage 미러 권한 · 매일 백업 항목이 그것을 보여 준다.

DB 가 필요한 테스트는 `scripts/dbtest` 로 돌린다(맥북 Docker 에 일회용 Postgres, 마이그레이션 전부,
각 서비스의 역할로).

`agent.choigonyok.com` 은 2026-10-07 에 DNS 와 두 터널의 ingress 에서 모두 지웠다. 맥미니의
예전 native cloudflared(`macmini` 터널)는 다른 프로젝트 호스트명 때문에 남아 있다.

**손님 계정.** Access 정책은 운영자와 손님 두 이메일을 허용한다. 콘솔은 Access 가
서명한 이메일로 역할을 정하고(`web/src/lib/role.ts`, Worker 비밀값 `ACCESS_OWNERS`/`ACCESS_GUESTS` - 이메일이라 공개 저장소에 두지 않는다), 손님은
대화·운동·캘린더·편집·위치(둘이 다닌 길, 읽기만 - 손님 폰 연결은 `OWNTRACKS_GUEST_USER`/`OWNTRACKS_GUEST_PASSWORD`, 맥미니 `.env`)·출발(자기 경로만 -
실시간 버스·지하철 키는 `DATA_GO_KR_KEY`/`SEOUL_SUBWAY_KEY`, 맥미니 `.env`)·작업(자기 작업·체크리스트만) 페이지와 `/api/agent`·`/api/jobs`·`/api/workout`·`/api/context/track`·`/api/context/owntracks/setup/guest`·`/api/map` 등 손님 몫 API, PhotoCraft 파일(`/photocraft/`)만 미들웨어에서 통과한다 - 나머지는 403
이거나 첫 화면으로 돌려보낸다. 손님의 대화는 별도 컨테이너 `agent-guest`(`JARVIS_ROLE=guest`)가
받는다: chat-svc 의 thread `guest`, 별도 CLI 홈·승인 카드, 캘린더·리마인드·체크리스트·작업 모듈,
내장 도구는 웹 검색뿐, 브라우저 토큰·자산·가계부 주소 없음. 리마인드·체크리스트(mind)와 작업(jobs)은
`JARVIS_GUEST_API_TOKEN` 으로 손님 것만 열리고(서로의 것은 '없음'), 손님 작업은 웹사이트를 못 연다(브라우저가
운영자 계정이라 jobs-svc 가 sites 를 거절) - 손님 에이전트의 스케줄러가 브라우저 없이 돌린다. 손님 리마인드는
손님 폰의 도착·출발(`location.guest.*`)과 `context` 의 `GET /now/guest` 로 울리고, 손님 기기로만 간다(대화 카드는 아직 없음). 게이트웨이 경로 `/agent-guest/…`(VPC `jarvis-agent-guest`, 포트 8081).
두 에이전트는 응답마다 `X-Jarvis-Agent: owner|guest` 를 붙이고, 게이트웨이는 경로와 맞지 않는
응답을 버리고 502 를 돌려준다. 같은 터널·같은 포트(8080)였을 때 Workers VPC 의 연결이 다른
에이전트에 붙는 일이 실제로 있었다(2026-10-08) - 그래서 포트도 나눴다.
손님 쪽 캘린더는 관점이 뒤집힌다: 손님 jarvis 의 캘린더 저장소(`FromPartnerSide`)가 나↔상대를
바꿔 읽고 쓰고, 손님이 만든 개인 일정은 calendar-svc 가 상대방(uniple partner) 이름으로 저장한다.
Google OAuth 앱이 테스트 모드라 손님 이메일을 Google Cloud 의 테스트 사용자에도 넣어야 한다.

**에이전트의 `/mcp` 와 `/intercept` 는 게이트웨이에서 막는다.** `/mcp` 는 Claude Code 가
루프백으로 부르는 도구 서버라 인증이 없다. 밖에서 닿으면 승인 카드를 건너뛰는 길이 된다.

**`/screen` 탭은 게이트웨이의 `/vnc/websockify` 로 붙는다.** 브라우저 웹소켓은 헤더를 못
붙이므로, 로그인한 콘솔이 `/api/screen/ticket` 에서 1분짜리 입장권(공유 토큰으로 서명)을
받아 주소에 붙인다. 게이트웨이는 이 경로에서만 입장권을 받는다. VPC 서비스 `jarvis-browser`
(browser:6080). 웹 Worker 의 `JARVIS_VNC_GATEWAY` 가 비면 예전처럼 호스트에 직접 붙는다.
VNC 서버(x11vnc)는 브라우저 컨테이너 안에서만 듣고(`-localhost`) 비밀번호(`VNC_PASSWORD` ←
맥미니 .env `JARVIS_VNC_PASSWORD`)를 묻는다. 입장권 경로가 그 비밀번호도 화면 탭에 건넨다.
비밀번호가 없으면 원격 화면은 열리지 않는다(브라우저는 그대로 돈다).

**로그인 정보 보관함(2026-10-17, vault-svc).** 맥미니 .env 에 `JARVIS_VAULT_KEY`(`openssl rand -base64 32` -
봉인 키, DB·백업에는 없으니 따로 보관), `JARVIS_VAULT_RELEASE_TOKEN`(브라우저가 꺼낼 때만 쓰는 토큰),
`JARVIS_DB_PASSWORD_VAULT`(`scripts/db-roles` 가 만든다). 맥미니 `JARVIS_ALLOWED_TOOLS` 에
`mcp__mcp-browser__browser_fill_login`(자동 입력은 카드 없이)과 `mcp__vault__list_logins`.

**맥미니 .env 에 더 필요한 값(2026-10-17).** `JARVIS_GUEST_API_TOKEN`(손님 agent 의 토큰 - chat·calendar·
events·memory 가 손님 쪽만 열어 준다), `JARVIS_MIND_CONSOLE_TOKEN`(mind 콘솔 로그인, `scripts/mini console`),
`JARVIS_VNC_PASSWORD`(8자). 앞의 둘은 `openssl rand -base64 48 | tr -d '/+='` 로 만든다.

---

이하는 2026-10-06 까지의 구성(에이전트를 터널 호스트명으로 공개)이다. 참고로 남긴다.

## 1. 두 개의 비밀을 먼저 만든다

```sh
openssl rand -hex 32   # JARVIS_API_TOKEN   - Worker와 agent가 나눠 갖는다
openssl rand -hex 32   # JARVIS_AUTH_SECRET - 세션 쿠키 서명 키
```

## 2. 서버 쪽: 터널을 붙여 agent를 내보낸다

Zero Trust 대시보드에서 터널을 만들고, public hostname 하나를
`http://agent:8080`으로 보낸다. 그 호스트명이 아래 `AGENT_URL`이 된다.

`jarvis/.env`:

```sh
CLAUDE_CODE_OAUTH_TOKEN=...
JARVIS_API_TOKEN=<1번에서 만든 첫 번째 값>
JARVIS_ALLOWED_ORIGIN=https://jarvis.choigonyok.com
CLOUDFLARE_TUNNEL_TOKEN=<Zero Trust가 준 터널 토큰>
```

```sh
docker compose --profile cloudflare up -d --build
```

`JARVIS_ALLOWED_ORIGIN`을 설정하면 agent는 `JARVIS_API_TOKEN` 없이는 아예 뜨지
않는다. 터널 호스트명은 공개 주소이므로, 토큰이 없으면 그 주소를 아는 사람은
로그인을 건너뛰고 어시스턴트를 그대로 쓸 수 있다.

`JARVIS_API_TOKEN` 은 이제 언제나 필요하다(2026-10-15). compose 가 빈 값을 거부하고, 서비스도
빈 `API_TOKEN` 으로는 뜨지 않는다 - 공개하지 않아도 compose 네트워크의 모든 컨테이너가 서로의
포트에 닿기 때문이다. 토큰 비교는 상수 시간이다. 로컬 시험만 `API_TOKEN_OPTIONAL=true`
(agent 는 `JARVIS_ALLOW_OPEN=true`). agent 의 `/mcp/*` 는 기동할 때 만든 내부 토큰을 CLI 의
MCP 설정에만 실어 보내므로, 다른 컨테이너에서는 401 이다.

## 3. Worker 쪽

```sh
cd web
npx wrangler login          # 한 번만. 브라우저가 열린다
```

비밀 네 개를 넣는다 (값은 프롬프트에 붙여넣는다):

```sh
npx wrangler secret put AGENT_URL             # https://<터널 호스트명>
npx wrangler secret put JARVIS_API_TOKEN      # 1번의 첫 번째 값
npx wrangler secret put JARVIS_AUTH_SECRET    # 1번의 두 번째 값
npx wrangler secret put JARVIS_AUTH_ID        # 로그인 아이디
npx wrangler secret put JARVIS_AUTH_PASSWORD  # 로그인 비밀번호
```

```sh
npm run cf:deploy
```

`wrangler.jsonc`의 `routes`가 `jarvis.choigonyok.com`을 custom domain으로 잡고
있으므로, 존이 같은 계정에 있으면 DNS 레코드와 인증서는 첫 배포에서 함께 만들어진다.
`workers_dev`는 꺼져 있다 — 이 콘솔은 로그인된 브라우저를 조종하므로 뒷문이 될
두 번째 공개 호스트명을 두지 않는다.

## 확인

```sh
curl -si https://jarvis.choigonyok.com/ | head -1          # 307 → /login
curl -si https://jarvis.choigonyok.com/api/agent/thread    # 401
```

## 로컬에서 Worker 런타임 그대로 돌려보기

```sh
cd web && npm run cf:preview
```

## 한계

- 음성 인식은 브라우저의 Web Speech API다. Chrome과 Safari에서 동작하고
  Firefox에서는 안 된다. 그 경우 음성 탭이 안내를 띄우고 채팅으로 돌아가면 된다.
- `/screen` 탭의 noVNC는 여전히 서버의 6080 포트를 직접 본다. 원격에서 쓰려면
  그 포트에도 터널 호스트명을 하나 더 주고 `JARVIS_VNC_URL`을 그리로 맞춰야 한다.
