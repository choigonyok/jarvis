-- jarvis 에서 들어오는 둘(운영자, 손님)을 둥지 계정에 잇는다.
-- jarvis 가 서명한 1분짜리 입장권(api/sso/jarvis)으로 들어오고, 비밀번호는 없다.

-- jarvis 로 처음 들어온 사람은 이름을 아직 정하지 않았다(0): 화면이 먼저 묻는다.
ALTER TABLE users ADD COLUMN named INTEGER NOT NULL DEFAULT 1;

CREATE TABLE sso_links (
  provider TEXT NOT NULL,
  subject TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id),
  PRIMARY KEY (provider, subject)
);

-- 한 번 쓴 입장권(다시 쓰지 못하게). 만료가 지나면 지운다.
CREATE TABLE sso_used (
  nonce TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL
);
