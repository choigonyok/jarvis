# Cloudflare 배포

## 무엇이 어디에 올라가는가 (2026-10-07부터)

| 조각 | 어디서 도는가 | 주소 |
| --- | --- | --- |
| `web` (Next.js 콘솔) | Cloudflare Workers | `jarvis.choigonyok.com` |
| `edge` (게이트웨이) | Cloudflare Workers | `jarvis-be.choigonyok.com` |
| 나머지 전부 (agent, browser, 각 서비스, postgres) | 맥미니의 docker compose | 공개 주소 없음 |

```
브라우저 ─▶ jarvis.choigonyok.com (web Worker, 로그인)
              │ fetch + Bearer JARVIS_API_TOKEN
              ▼
           jarvis-be.choigonyok.com (edge Worker)
              │ 토큰 없으면 401, agent 는 콘솔이 쓰는 경로만
              │ Workers VPC 바인딩 (jarvis-agent 터널)
              ▼
           맥미니 compose: agent:8080 · workout:8091 · assets:8092
                           spending:8095 · kakaotalk:8090 · status:8096
```

맥미니에서 인터넷을 향해 열린 포트는 없다. 터널(`tunnel` 서비스, `--profile cloudflare`)은
바깥으로 나가는 연결만 맺고, 공개 ingress 도 두지 않는다. Workers VPC 서비스가 그 터널을
타고 compose 이름(`agent` 등)으로 각 서비스에 닿는다.

- VPC 서비스: `npx wrangler vpc service list` (jarvis-agent … jarvis-status, 터널 `5b824fd4…`)
- 게이트웨이 배포: `cd web && npx wrangler deploy -c ../edge/wrangler.jsonc`
- 게이트웨이 비밀: `npx wrangler secret put JARVIS_API_TOKEN -c ../edge/wrangler.jsonc`
- 콘솔 배포: `cd web && npm run cf:build && npm run cf:deploy`. 백엔드 주소는
  `web/wrangler.jsonc` 의 `vars`(AGENT_URL 만 비밀).
- 맥미니 쪽: `scripts/mini up`, `scripts/mini compose --profile cloudflare up -d tunnel`.
  `web` 컨테이너는 `web` 프로필이라 맥미니에서는 뜨지 않는다.

**`jarvis.choigonyok.com` 앞에는 Cloudflare Access 가 있다.** Zero Trust 앱 `jarvis`
(팀 `choigonyok.cloudflareaccess.com`, Free 플랜), 정책 `only me` 는 운영자 이메일 하나만 허용하고
로그인은 Google(OAuth 클라이언트는 운영자의 Google Cloud 프로젝트 jarvis-access, 테스트 사용자 본인만)과 예비로 이메일 일회용 코드. 앱·팀 세션 모두 730h(약 1개월). 그 뒤에 콘솔 자체 로그인이 한 번 더 있다.
게이트웨이(`jarvis-be`)는 Access 대상이 아니다 - 사람이 아니라 콘솔 Worker 가 부르는 곳이고,
`JARVIS_API_TOKEN` 이 그 문을 지킨다.

`agent.choigonyok.com` 은 2026-10-07 에 DNS 와 두 터널의 ingress 에서 모두 지웠다. 맥미니의
예전 native cloudflared(`macmini` 터널)는 다른 프로젝트 호스트명 때문에 남아 있다.

**에이전트의 `/mcp` 와 `/intercept` 는 게이트웨이에서 막는다.** `/mcp` 는 Claude Code 가
루프백으로 부르는 도구 서버라 인증이 없다. 밖에서 닿으면 승인 카드를 건너뛰는 길이 된다.

**`/screen` 탭은 게이트웨이의 `/vnc/websockify` 로 붙는다.** 브라우저 웹소켓은 헤더를 못
붙이므로, 로그인한 콘솔이 `/api/screen/ticket` 에서 1분짜리 입장권(공유 토큰으로 서명)을
받아 주소에 붙인다. 게이트웨이는 이 경로에서만 입장권을 받는다. VPC 서비스 `jarvis-browser`
(browser:6080). 웹 Worker 의 `JARVIS_VNC_GATEWAY` 가 비면 예전처럼 호스트에 직접 붙는다.

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
