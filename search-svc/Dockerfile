# Static, CGO-free binary.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/search-svc .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/search-svc /app/search-svc
ENV LISTEN_ADDR=:8104
EXPOSE 8104
# 상태가 없다(캐시는 메모리에만). 바깥 웹페이지를 받아 읽는 프로세스라 더더욱 root 로 돌지 않는다.
USER 65532:65532
ENTRYPOINT ["/app/search-svc"]
