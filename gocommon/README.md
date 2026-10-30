# gocommon

Go code more than one service shares, kept in one place instead of copied.

- `notify` - reporting events to the stream through `input.outbox`
  (used by agent, calendar-svc, context-svc, mind-svc, status-svc).
- `queue` - the stream: `Consumer` reads a topic with SKIP LOCKED, keeps order
  per subject and the cursor at the low-water mark, and applies the `health`
  rule to what fails. input-svc publishes with `Deliver` and `Wake`. There is
  no process behind it (agent, mind-svc, output-svc read it; input-svc writes).
- `collect` - the message collectors' bearer check and retention loop
  (kakaotalk-client, imessage-client).
- `record` - writing what happened as Records to `input.outbox` (see db/README),
  beside the work and never in its way: `Write` only queues, one goroutine
  writes in order and waits out an outage, and its loop shows on `/health`.
  `NewMemory` is the same Writer for a test or a replay. Used by agent.
- `health` - the shape every service's `GET /health` answers in (its
  background loops: last round that got through, last error; and its held
  box), and the one rule for an item that keeps failing: an outage of the
  other side costs it no try and the queue waits in order; failing on its own
  `MaxTries` (5) times, it is held - set aside with why, the cursor moves on,
  /health counts it so the status tab shows it, and it goes again once when
  the other side works again or a person asks. Used by `queue`, input-svc,
  mind-svc, output-svc; status-svc reads it.

Each service's `go.mod` points at it with `replace ... => ../gocommon`, and its
Docker build receives it as the extra build context `gocommon`
(`additional_contexts` in docker-compose.yml).
