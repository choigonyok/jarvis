# Static, CGO-free binary. pgx is pure Go, so there is no libpq to link and
# the final stage needs nothing but certificates.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/spending-svc .

FROM alpine:3.20
# tzdata: 날짜를 찍는 조회가 KST 여야 한다. zoneinfo 가 바이너리에 내장되지
# 않는 조합이 있어 이미지에도 둔다.
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/spending-svc /app/spending-svc
ENV LISTEN_ADDR=:8095
EXPOSE 8095
# 데이터가 볼륨이 아니라 Postgres 에 있으므로 이 이미지는 상태가 없다.
ENTRYPOINT ["/app/spending-svc"]
