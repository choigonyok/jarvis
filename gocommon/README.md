# gocommon

Go code more than one service shares, kept in one place instead of copied.

- `notify` - reporting events to events-svc through the `event_outbox` table
  (used by agent, calendar-svc, context-svc, spending-svc, status-svc).
- `collect` - the message collectors' bearer check and retention loop
  (kakaotalk-client, imessage-client).

Each service's `go.mod` points at it with `replace ... => ../gocommon`, and its
Docker build receives it as the extra build context `gocommon`
(`additional_contexts` in docker-compose.yml).
