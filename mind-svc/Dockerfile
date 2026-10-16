# Static, CGO-free binary. pgx is pure Go; the console is embedded.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mind-svc .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/mind-svc /app/mind-svc
ENV LISTEN_ADDR=:8102
EXPOSE 8102
# 볼륨이 없으므로 root 로 돌 이유가 없다.
USER 65532:65532
ENTRYPOINT ["/app/mind-svc"]
