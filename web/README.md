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

## 올리기

```sh
npm run cf:build && npm run cf:deploy
```

`cf:deploy` 는 빌드하지 않고 있는 `.open-next` 를 올린다. 늘 `cf:build` 가 먼저다.
