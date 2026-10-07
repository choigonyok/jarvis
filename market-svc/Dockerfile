# Static, CGO-free binary. pgx is pure Go, so there is no libpq to link and
# the final stage needs nothing but certificates.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/market-svc .

FROM alpine:3.20
# tzdata: 화면에 찍는 등록·판매 시각이 KST 여야 한다.
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/market-svc /app/market-svc
ENV LISTEN_ADDR=:8097
EXPOSE 8097
# 데이터가 볼륨이 아니라 Postgres 에 있으므로 이 이미지는 상태가 없다.
ENTRYPOINT ["/app/market-svc"]
