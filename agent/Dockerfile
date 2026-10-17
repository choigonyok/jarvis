# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
# go.mod 의 replace => ../gocommon 이 가리키는 곳. compose 의 additional_contexts 로 온다.
COPY --from=gocommon . /gocommon
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

# Node is not optional here: the agent drives the Claude Code CLI, and that
# CLI is what bills against the subscription.
FROM node:22-alpine
# Pinned: what the CLI caches and for how long (CLAUDE_CODE_PROMPT_CACHE_TTL),
# its default model, and whether a resumed session takes a new system prompt
# all change between versions, and every image build used to take whatever
# was newest. 2.1.292 is what ran until this was pinned. To move it, build
# with --build-arg CLAUDE_CODE_VERSION=… and compare the agent's "턴 사용량"
# and "백그라운드 사용량" log lines before and after.
ARG CLAUDE_CODE_VERSION=2.1.292
RUN apk add --no-cache ca-certificates git ripgrep bash \
 && npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm cache clean --force \
 && adduser -D -u 10001 jarvis \
 && mkdir -p /workspace /home/jarvis /data /uploads \
 && chown -R jarvis:jarvis /workspace /home/jarvis /data /uploads

COPY --from=build /out/agent /usr/local/bin/agent
USER jarvis
WORKDIR /workspace
# Module data lives outside /workspace on purpose: inside it, the CLI's own
# Read and Write tools would be a second door past the module's tools.
ENV JARVIS_ADDR=:8080 \
    JARVIS_WORKSPACE=/workspace \
    JARVIS_HOME=/home/jarvis \
    JARVIS_PROPOSALS_PATH=/data/proposals.json \
    JARVIS_SESSION_PATH=/data/session.json \
    JARVIS_MCP_URL=http://127.0.0.1:8080/mcp
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["agent"]
