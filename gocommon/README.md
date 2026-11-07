# gocommon

Go code more than one service shares, kept in one place instead of copied.

## What belongs here

A **mechanism** the services share: how a service starts, listens and stops,
how it says it is healthy, how it writes to the stream and reads it, how it
seals. Copies of a mechanism drift - one service waits for its database and
the next does not, one server has a write limit and the next has none - and a
fix made in one copy is missing from the others.

A **domain rule** only when two or more services must agree on it byte for
byte: a name one of them writes and another matches, a table both read, a cut
both make the same way. If one decided it differently the other would break.
A rule only one service uses stays in that service, however reusable it looks.
These four are here under that rule, and stay:

- `pay` - the payments whose password the vault keeps, in one table
  (`pins.json`): vault-svc saves a password only for a site in it, the agent
  asks for one by its name, and the browser's keypads are checked against the
  same file (`browser/test_pins_table.py`).
- `site` - the registrable domain a login belongs to, by the Public Suffix
  List: vault-svc binds a saved login to it, and the agent cuts the host of the
  page it is on the same way to ask for that login.
- `person` - whose a thing is: `owner` and `guest`, the names input-svc,
  mind-svc and output-svc write into rows and streams and match on.
- `weather` - the shape of the day's weather: context-svc writes the forecasts
  as records in it, and mind-svc reads them.

## The packages

- `svc` - what every service's main does the same way: settings from the
  environment (`Env`, `Int`, `Duration`, `Float`; `Need` and `Token` stop a
  service that lacks what it cannot run without), the services' bearer token
  (`Bearer`, `Guard`), answers in JSON (`WriteJSON`, `Error`), and `Serve` -
  listen with the service's own read and write limits until SIGINT or SIGTERM
  (`Signals`), then let what is in flight finish. A handler that waits as long
  as the write limit moves its own deadline (`Extend`); an event stream lifts
  it (`Stream`). Used by every Go service but agent and assets-svc.
- `svc/pg` - Postgres for a main: `Open` waits for the database (30 tries, a
  second apart) before the service goes on, `Retry` does the same for a store's
  own opener, and `Lazy` is for status-svc, which watches the database rather
  than needs it. Apart from `svc`, so a service without a database does not
  build pgx in.
- `health` - the shape every service's `GET /health` answers in (its
  background loops: last round that got through, last error; its held box;
  why it cannot work at all, with a 503; and what else it says of itself,
  never judged), and the one rule for an item that keeps failing: an outage of
  the other side costs it no try and the queue waits in order; failing on its
  own `MaxTries` (5) times, it is held - set aside with why, the cursor moves
  on, /health counts it so the status tab shows it, and it goes again once
  when the other side works again or a person asks. Used by `queue`, `record`,
  `watch` and the services' `/health`; status-svc reads it.
- `queue` - the stream: `Consumer` reads a topic with SKIP LOCKED, keeps order
  per subject and the cursor at the low-water mark, and applies the `health`
  rule to what fails. input-svc publishes with `Deliver` and `Wake`. There is
  no process behind it (agent, mind-svc, output-svc read it; input-svc writes).
- `record` - writing what happened as Records to `input.outbox` (see db/README),
  beside the work and never in its way: `Write` only queues, one goroutine
  writes in order and waits out an outage, and its loop shows on `/health`.
  `NewMemory` is the same Writer for a test or a replay.
- `notify` - reporting events to the stream through `input.outbox`
  (agent, calendar-svc, context-svc, mind-svc, status-svc).
- `watch` - an adapter's loop: read the outside every so often, and leave a
  record when what it read has changed (calendar-svc, context-svc, photos-svc).
- `checks` - how a service says how something stands that only it can see (a
  login, a disk); status-svc shows the latest of each.
- `collect` - the message collectors' bearer check, retention loop and
  publisher (kakaotalk-client, imessage-client).
- `seal` - what keeps text from lying about in the clear (AES-256-GCM): a
  message's text from the collectors on through input-svc and mind-svc's
  store, and vault-svc's logins. One form, `jsl2 | key id | nonce | sealed`,
  bound to its place (a login's domain, or nothing); the two forms without the
  prefix that stored rows hold still open.

Each service's `go.mod` points at it with `replace ... => ../gocommon`, and its
Docker build receives it as the extra build context `gocommon`
(`additional_contexts` in docker-compose.yml).
