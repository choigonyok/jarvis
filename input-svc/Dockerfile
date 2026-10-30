# Static, CGO-free binary. pgx is pure Go.
FROM golang:1.25-alpine AS build
WORKDIR /src
# go.mod 의 replace => ../gocommon 이 가리키는 곳. compose 의 additional_contexts 로 온다.
COPY --from=gocommon . /gocommon
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/input-svc .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/input-svc /app/input-svc
ENV LISTEN_ADDR=:8101
EXPOSE 8101
# 볼륨이 없으므로 root 로 돌 이유가 없다.
USER 65532:65532
ENTRYPOINT ["/app/input-svc"]
