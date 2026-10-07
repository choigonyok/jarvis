# TypeScript 를 컴파일해서 순수 JS 를 돌린다. Node 의 타입 스트리핑에 맡기면
# 런타임 버전이 올라갈 때 동작이 바뀔 여지가 생기고, 이 서비스는 주문을 낼 수
# 있는 키를 들고 있다 - 예측 가능한 쪽이 낫다.
FROM node:22-alpine AS build
WORKDIR /src
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY src ./src
RUN npx tsc

FROM node:22-alpine
# tzdata: 스냅샷의 날짜가 KST 여야 한다. UTC 로 찍으면 한국 시간 자정~09시의
# 조회가 전날 스냅샷을 덮어쓴다.
RUN apk add --no-cache tzdata
WORKDIR /app
ENV NODE_ENV=production
# 런타임 의존성만. typescript 와 타입 패키지는 이미지에 남을 이유가 없다.
COPY package.json package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY --from=build /src/dist ./dist
ENV PORT=8092
EXPOSE 8092
USER node
CMD ["node", "dist/index.js"]
