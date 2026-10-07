# Build a static, CGO-free binary. The source database is read through the
# sqlcipher CLI (installed in the final stage), and the local store uses the
# pure-Go modernc.org/sqlite driver, so no C toolchain is needed here.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kakaotalk-client .

FROM alpine:3.20
# sqlcipher: unlocks the KakaoTalk database. ca-certificates for good measure.
RUN apk add --no-cache sqlcipher ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/kakaotalk-client /app/kakaotalk-client
# Store lives on a mounted volume; scratch is ephemeral.
ENV STORE_PATH=/data/kakaotalk.db \
    KAKAO_SCRATCH_DIR=/tmp/kkc-scratch \
    LISTEN_ADDR=:8090 \
    POLL_INTERVAL=1s
EXPOSE 8090
ENTRYPOINT ["/app/kakaotalk-client"]
