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
| `KAKAO_IMPORT_HISTORY` | `true` | Once, copy the rows the source held before the store's first run (a fresh store starts at the tail). They sit below every reader's `/messages/after` cursor, so nothing reads them as new. |
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

## Logged back in after a logout (`cmd/relogin`)

KakaoTalk on the Mac mini logs itself out when the network comes back after a
long drop (twelve minutes offline on 2026-10-19 and 2026-10-22), and a
logged-out KakaoTalk writes nothing: every message stops without an error.
`cmd/relogin` watches KakaoTalk through the macOS accessibility API from a
LaunchAgent on the Mac mini (it cannot run in a container - the app's
windows are only reachable from the logged-in session):

- every five seconds it reads KakaoTalk's own app menu: its 로그아웃 can be
  chosen only while KakaoTalk is logged in (the Apple menu's "<user>
  로그아웃…" is never looked at). Not logged in, it reads the windows; a
  notice about the logout gets its 확인 (only such a notice, or any while the
  login window is up),
- the login window gets the password kept in the login keychain and its
  로그인 - the moment Kakao's servers answer again, and again 10 s after a
  failure, a little longer each time but never more than 5 minutes apart. A
  refused password stops it at once (tried on and on, it locks the account),
  and so does a screen asking for the phone; KakaoTalk's own 자동 로그인 is
  ticked while the window is open,
- what it saw and did goes to `~/.jarvis-host/kakaotalk-login.json`, which the
  상태 tab reads as "카톡 로그인".

**The Mac mini's screen must not lock.** While it is locked, macOS hands an
accessibility client the app itself in place of every window - KakaoTalk's,
Terminal's, Safari's alike - so no login window can be read and nothing is
tried; only the menus still answer. 2026-10-23 the screen had locked the night
before, KakaoTalk was logged out by a 13-minute drop at 14:38, and it read as
logged in until the screen was unlocked at 02:10. So the lock screen is off on
the Mac mini (System Settings → Lock Screen → require password after the
screen saver or display: never; it logs in by itself at boot and FileVault is
off, so this opens nothing new). Should it lock anyway, the record says
`locked` (logged out: the 상태 tab fails and pushes) or `locked-in` (logged in,
but a logout would wait: a warning).

```bash
scripts/kakaotalk-relogin install    # from the MacBook: build, wrap as an app, sign, LaunchAgent on the Mac mini
scripts/kakaotalk-relogin password   # type the Kakao account and password here; it goes to the Mac mini's keychain
scripts/kakaotalk-relogin status     # state and the last lines of the log
scripts/kakaotalk-relogin inspect    # the shape of KakaoTalk's windows, for when its login window changes
```

It is installed as `/Applications/JarvisKakaoRelogin.app` ("Jarvis 카톡 자동
로그인") and signed with this Mac's Apple Development certificate
(`JARVIS_CODESIGN_IDENTITY` overrides), so a rebuild is the same program to
macOS. Once, at the Mac mini: allow it under System Settings → Privacy &
Security → Accessibility (macOS asks when it first starts; otherwise + →
Applications). Ad-hoc signed (no certificate), every install needs that
permission and the password again.

