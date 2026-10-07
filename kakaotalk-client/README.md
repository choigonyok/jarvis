# kakaotalk-client

Near-real-time collector for the local **KakaoTalk (macOS)** database. It polls
the on-disk SQLCipher database once a second, stores new messages incrementally
in its own plain SQLite file, and serves them over a small read-only HTTP API
for the jarvis web console.

It never writes to the KakaoTalk database. Each poll copies the live
`main + -wal + -shm` files into a scratch dir and reads the copy, so the running
app is never disturbed and WAL-resident (newest) messages are always seen.

## How it works

1. **Locate** the `^[0-9a-f]{78}$` database in the KakaoTalk container.
2. **Resolve identity** — `IOPlatformUUID` (via `ioreg`) + the account id
   (from the KakaoTalk plist, or a SHA-512 recovery scan, or `KAKAO_USER_ID`).
3. **Verify** the pair by checking the derived filename equals the real one.
4. **Derive the key** — PBKDF2-HMAC-SHA256, 100k rounds → 256-hex passphrase.
5. **Read** through the `sqlcipher` CLI with `cipher_compatibility=3`
   (KakaoTalk's SQLCipher-3 parameter set), incremental on `ROWID` (indexed).
6. **Store & serve** — upsert into local SQLite, expose over HTTP.

The incremental cursor (source `ROWID`) is persisted, so a restart resumes where
it left off. A fresh store starts from the current tail (no full-history
backfill).

## Configuration (environment)

| Var | Default | Meaning |
|-----|---------|---------|
| `POLL_INTERVAL` | `1s` | Poll cadence. 250ms floor; below is waste. |
| `LISTEN_ADDR` | `:8090` | HTTP bind address. |
| `STORE_PATH` | `./data/kakaotalk.db` | Local SQLite store. |
| `API_TOKEN` | – | Bearer token guarding all routes but `/health`. |
| `KAKAO_UUID` | `ioreg` | IOPlatformUUID (required in a container). |
| `KAKAO_USER_ID` | plist/recover | Account id; set it to skip the recovery scan. |
| `KAKAO_SQLCIPHER_KEY` | derived | Pre-derived passphrase; overrides UUID+id. |
| `KAKAO_CONTAINER_DIR` | standard | KakaoTalk Application Support dir. |
| `KAKAO_PLIST_DIR` | standard | KakaoTalk Preferences dir (for id recovery). |
| `KAKAO_SCRATCH_DIR` | tmp | Where live files are copied before reading. |
| `SqlcipherBin` (`KAKAO_*`) | `sqlcipher` | Handled internally; CLI must be on PATH. |

## API

All routes are GET and read-only. Send `Authorization: Bearer <API_TOKEN>`.

| Route | Description |
|-------|-------------|
| `GET /health` | Liveness (no auth). |
| `GET /status` | Poller status: cursor, totals, last latency, last error. |
| `GET /chats?limit=` | Rooms, most recent first. |
| `GET /messages?chat=<id>&since=<epoch>&limit=` | Messages in one room. |
| `GET /messages/recent?limit=` | Newest messages across all rooms. |
| `GET /search?q=&limit=` | Substring search over stored text. |

## Run locally (host)

```bash
go build -o kakaotalk-client .
brew install sqlcipher
KAKAO_USER_ID=<id> STORE_PATH=./data/kakaotalk.db ./kakaotalk-client
```

Running on the host is the most reliable path — it reads the live `-wal` as
messages arrive and needs no UUID (it calls `ioreg` itself).

## Run with Docker Compose

Set `KAKAO_UUID` and `KAKAO_USER_ID` (or `KAKAO_SQLCIPHER_KEY`) in `.env`, then:

```bash
docker compose up -d kakaotalk
```

The KakaoTalk container directory is bind-mounted read-only. The web console
reaches it at `http://kakaotalk:8090` and proxies through `/api/kakaotalk/*`.

> macOS only. KakaoTalk must have been open for messages to be in the local DB;
> incoming messages while the app is connected are written immediately (no need
> to open the room), but history from while it was closed only syncs when you
> open that chat.
