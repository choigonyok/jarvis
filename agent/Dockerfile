# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

# Node is not optional here: the agent drives the Claude Code CLI, and that
# CLI is what bills against the subscription.
FROM node:22-alpine
RUN apk add --no-cache ca-certificates git ripgrep bash \
 && npm install -g @anthropic-ai/claude-code \
 && npm cache clean --force \
 && adduser -D -u 10001 jarvis \
 && mkdir -p /workspace /home/jarvis /data \
 && chown -R jarvis:jarvis /workspace /home/jarvis /data

COPY --from=build /out/agent /usr/local/bin/agent
USER jarvis
WORKDIR /workspace
# Module data lives outside /workspace on purpose: inside it, the CLI's own
# Read and Write tools would be a second door past the module's tools.
ENV JARVIS_ADDR=:8080 \
    JARVIS_WORKSPACE=/workspace \
    JARVIS_HOME=/home/jarvis \
    JARVIS_CALENDAR_PATH=/data/calendar.json \
    JARVIS_MCP_URL=http://127.0.0.1:8080/mcp
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["agent"]
