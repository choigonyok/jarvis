# Build a static, CGO-free binary. The source database is read through the
# sqlcipher CLI (installed in the final stage), and the local store uses the
# pure-Go modernc.org/sqlite driver, so no C toolchain is needed here.
FROM golang:1.25-alpine AS build
WORKDIR /src
# go.mod 의 replace => ../gocommon 이 가리키는 곳. compose 의 additional_contexts 로 온다.
COPY --from=gocommon . /gocommon
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kakaotalk-client .

FROM alpine:3.22
# sqlcipher: unlocks the KakaoTalk database. ca-certificates for good measure.
RUN apk add --no-cache sqlcipher ca-certificates tzdata && mkdir -p /data && chown 65532:65532 /data
WORKDIR /app
COPY --from=build /out/kakaotalk-client /app/kakaotalk-client
# Store lives on a mounted volume; scratch is ephemeral.
ENV STORE_PATH=/data/kakaotalk.db \
    KAKAO_SCRATCH_DIR=/tmp/kkc-scratch \
    LISTEN_ADDR=:8090 \
    POLL_INTERVAL=1s
EXPOSE 8090
# root 로 돌 이유가 없다: 카톡 원본은 읽기 전용 마운트이고, /data 는 이 사용자의 것이다
# (예전 볼륨은 DEPLOY.md 의 소유자 변경을 한 번 한다).
USER 65532:65532
ENTRYPOINT ["/app/kakaotalk-client"]
