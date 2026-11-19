-- 둥지: 커플 한 쌍이 방 하나(couples)를 함께 쓴다.

CREATE TABLE couples (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  intro TEXT NOT NULL DEFAULT '',
  since TEXT,
  coins INTEGER NOT NULL DEFAULT 300 CHECK (coins >= 0),
  storage_slots INTEGER NOT NULL DEFAULT 4,
  wallpaper TEXT NOT NULL DEFAULT 'paper_plain',
  floor TEXT NOT NULL DEFAULT 'floor_wood',
  themes TEXT NOT NULL DEFAULT '["paper_plain","floor_wood"]',
  invite_code TEXT UNIQUE,
  listed INTEGER NOT NULL DEFAULT 1,
  dirt_slot INTEGER NOT NULL,
  visits_total INTEGER NOT NULL DEFAULT 0,
  active_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX couples_active ON couples (listed, active_at DESC);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  login TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  pass_hash TEXT NOT NULL,
  pass_salt TEXT NOT NULL,
  couple_id TEXT NOT NULL REFERENCES couples (id),
  created_at INTEGER NOT NULL
);
CREATE INDEX users_couple ON users (couple_id);

-- 한 둥지에는 둘까지만 산다(같은 초대 코드로 동시에 들어와도).
CREATE TRIGGER couple_two BEFORE INSERT ON users
BEGIN
  SELECT RAISE(ABORT, 'home_full')
  WHERE (SELECT COUNT(*) FROM users WHERE couple_id = NEW.couple_id) >= 2;
END;

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);

CREATE TABLE login_fails (
  login TEXT PRIMARY KEY,
  n INTEGER NOT NULL,
  since INTEGER NOT NULL
);

-- 방에 놓였거나(room) 창고에 든(storage) 물건. 화분·어항은 state 에 자라는 값(JSON).
CREATE TABLE items (
  id TEXT PRIMARY KEY,
  couple_id TEXT NOT NULL REFERENCES couples (id),
  key TEXT NOT NULL,
  place TEXT NOT NULL CHECK (place IN ('room', 'storage')),
  x INTEGER NOT NULL DEFAULT 0,
  y INTEGER NOT NULL DEFAULT 0,
  flip INTEGER NOT NULL DEFAULT 0,
  wall TEXT CHECK (wall IN ('left', 'right')),
  state TEXT,
  stored_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX items_couple ON items (couple_id, place);

-- 창고 칸 수를 넘겨 넣으려 하면 그 쓰기 전체가 취소된다.
CREATE TRIGGER storage_cap_insert BEFORE INSERT ON items
WHEN NEW.place = 'storage'
BEGIN
  SELECT RAISE(ABORT, 'storage_full')
  WHERE (SELECT COUNT(*) FROM items WHERE couple_id = NEW.couple_id AND place = 'storage')
     >= (SELECT storage_slots FROM couples WHERE id = NEW.couple_id);
END;

CREATE TRIGGER storage_cap_update BEFORE UPDATE OF place ON items
WHEN NEW.place = 'storage' AND OLD.place <> 'storage'
BEGIN
  SELECT RAISE(ABORT, 'storage_full')
  WHERE (SELECT COUNT(*) FROM items WHERE couple_id = NEW.couple_id AND place = 'storage')
     >= (SELECT storage_slots FROM couples WHERE id = NEW.couple_id);
END;

CREATE TABLE fish (
  id TEXT PRIMARY KEY,
  couple_id TEXT NOT NULL,
  tank_id TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  species TEXT NOT NULL,
  name TEXT NOT NULL,
  hunger REAL NOT NULL,
  growth REAL NOT NULL,
  hungry_at INTEGER,
  t INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX fish_couple ON fish (couple_id);

CREATE TABLE dirt (
  id TEXT PRIMARY KEY,
  couple_id TEXT NOT NULL,
  x INTEGER NOT NULL,
  y INTEGER NOT NULL,
  kind TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX dirt_couple ON dirt (couple_id);

-- 커플 일기: 하루에 한 사람이 한 편. 둘만 본다.
CREATE TABLE diary (
  couple_id TEXT NOT NULL,
  day TEXT NOT NULL,
  author_id TEXT NOT NULL,
  mood TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (couple_id, day, author_id)
);

CREATE TABLE diary_comments (
  id TEXT PRIMARY KEY,
  couple_id TEXT NOT NULL,
  day TEXT NOT NULL,
  author_id TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX diary_comments_day ON diary_comments (couple_id, day);

-- 방명록: 다른 둥지의 사람이 남긴다. 비밀글은 쓴 사람과 그 둥지의 둘만 본다.
CREATE TABLE guestbook (
  id TEXT PRIMARY KEY,
  home_id TEXT NOT NULL,
  author_id TEXT NOT NULL,
  body TEXT NOT NULL,
  secret INTEGER NOT NULL DEFAULT 0,
  reply TEXT,
  reply_by TEXT,
  reply_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX guestbook_home ON guestbook (home_id, created_at DESC);

-- 하루에 한 번씩 센 방문. helped 는 그날 치워 준 먼지 수.
CREATE TABLE visits (
  home_id TEXT NOT NULL,
  visitor_id TEXT NOT NULL,
  day TEXT NOT NULL,
  helped INTEGER NOT NULL DEFAULT 0,
  at INTEGER NOT NULL,
  PRIMARY KEY (home_id, visitor_id, day)
);
CREATE INDEX visits_visitor ON visits (visitor_id, at DESC);

-- 하루에 몇 번까지만 받는 깃털(출석, 일기, 방명록)
CREATE TABLE claims (
  user_id TEXT NOT NULL,
  day TEXT NOT NULL,
  kind TEXT NOT NULL,
  n INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day, kind)
);

CREATE TABLE ledger (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  couple_id TEXT NOT NULL,
  delta INTEGER NOT NULL,
  reason TEXT NOT NULL,
  by_id TEXT,
  at INTEGER NOT NULL
);
CREATE INDEX ledger_couple ON ledger (couple_id, at DESC);
