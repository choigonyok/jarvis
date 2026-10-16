# Static, CGO-free binary. pgx is pure Go.
FROM golang:1.25-alpine AS build
WORKDIR /src
# go.mod 의 replace => ../gocommon 이 가리키는 곳. compose 의 additional_contexts 로 온다.
COPY --from=gocommon . /gocommon
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/vault-svc .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/vault-svc /app/vault-svc
ENV LISTEN_ADDR=:8105
EXPOSE 8105
# 상태는 전부 Postgres(봉인된 값)와 환경변수(키)에 있다. root 로 돌 이유가 없다.
USER 65532:65532
ENTRYPOINT ["/app/vault-svc"]
