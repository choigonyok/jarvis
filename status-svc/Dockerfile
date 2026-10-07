# Static, CGO-free binary. pgx is pure Go.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/status-svc .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/status-svc /app/status-svc
ENV LISTEN_ADDR=:8096
EXPOSE 8096
ENTRYPOINT ["/app/status-svc"]
