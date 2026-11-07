# web - Jarvis 콘솔

대화, 결재, 그리고 오늘·돈·생활 화면을 띄우는 Next.js 앱이다. Cloudflare Workers(OpenNext)에 올라간다.

- 무엇을 왜 이렇게 그리는지: [DESIGN.md](DESIGN.md)
- 전체 구조와 서비스: [../README.md](../README.md)
- 배포와 비밀 값: [../DEPLOY.md](../DEPLOY.md)

## 로컬에서 띄우기

```sh
npm install
npm run dev
```

`next dev` 는 백엔드를 부를 서비스 바인딩이 없다. 맥북에서는 로컬 compose 를 띄우고
`AGENT_URL=http://localhost:8080` 처럼 `.env.local` 로 가리킨다(자세한 것은 DEPLOY.md).
화면만 볼 때는 `npm run build && npm run start` 가 `next dev` 보다 운영과 가깝다.

## 에이전트의 타입

대화와 카드처럼 에이전트가 보내는 것의 모양(`src/lib/gen/`)은 에이전트의 Go 구조체에서 tygo 로 만든다
(`scripts/gen-types.sh`, 어느 패키지를 어디에 쓰는지는 `scripts/tygo.yaml`). 그 구조체를 바꾸면 다시 쓰고,
쓰인 것을 같이 커밋한다. Go 가 있어야 한다.

```sh
npm run gen:types              # src/lib/gen/ 을 다시 쓴다
npm run gen:types -- --check   # 아무것도 고치지 않고, 구조체와 다르면 실패한다
```

## 올리기

```sh
npm run cf:build && npm run cf:deploy
```

`cf:build` 는 먼저 `gen:types -- --check` 를 돌린다. `src/lib/gen/` 이 에이전트의 Go 구조체와 다르면 빌드하지
않고 멈추니, `npm run gen:types` 로 다시 쓰고 커밋한 뒤 다시 빌드한다. 그래서 배포를 빌드하는 맥에는 Go 가
있어야 한다(없으면 그렇게 말하고 멈춘다).

`cf:deploy` 는 빌드하지 않고 있는 `.open-next` 를 올린다. 늘 `cf:build` 가 먼저다.
