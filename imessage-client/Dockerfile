# Static, CGO-free binary: the pure-Go modernc.org/sqlite driver reads both the
# copied Messages database and the local store.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/imessage-client .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && mkdir -p /data
WORKDIR /app
COPY --from=build /out/imessage-client /app/imessage-client
ENV STORE_PATH=/data/imessage.db \
    SCRATCH_DIR=/tmp/imessage-scratch \
    CHAT_DB=/messages/chat.db \
    LISTEN_ADDR=:8099
EXPOSE 8099
ENTRYPOINT ["/app/imessage-client"]
