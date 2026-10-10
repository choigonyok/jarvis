# Static, CGO-free binary. pgx is pure Go.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/events-svc .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/events-svc /app/events-svc
ENV LISTEN_ADDR=:8101
EXPOSE 8101
ENTRYPOINT ["/app/events-svc"]
