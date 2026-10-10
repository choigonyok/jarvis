# Static, CGO-free binary. pgx and webpush-go are pure Go.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notify-svc .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/notify-svc /app/notify-svc
ENV LISTEN_ADDR=:8097
EXPOSE 8097
ENTRYPOINT ["/app/notify-svc"]
