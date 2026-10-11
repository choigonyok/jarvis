-- mind: 인지 루프 실험 서비스(mind-svc)의 저장소. 감지 → 관측 로그(events) →
-- 해석 → 세계 모델 → 주의·스킬 → 자율성 관문 → 행동.

-- 관측한 메시지의 원문. 관측 로그(events)에는 ref 만 남고 본문은 여기에
-- 둔다. MIND_CONTENT_KEY 가 있으면 AES-GCM 으로 봉해서(sealed) 저장한다.
create table if not exists mind_messages (
  ref        text primary key,             -- kakao:3215, imessage:11, sim:42
  source     text not null,
  room       text not null,
  room_name  text not null default '',
  sender     text not null default '',
  is_mine    boolean not null,
  sent_at    timestamptz not null,
  body       bytea not null,
  sealed     boolean not null default false,
  created_at timestamptz not null default now()
);
create index if not exists mind_messages_room on mind_messages (room, sent_at);

-- 대화방마다 어디까지 해석했는지.
create table if not exists mind_rooms (
  room        text primary key,
  interpreted timestamptz not null
);

-- 세계 모델: 일정(plan)과 열린 고리(loop). state 는 지금 믿는 것, version 은
-- 동시에 고치는 것을 막는 낙관적 잠금.
create table if not exists mind_entities (
  id         text primary key,
  kind       text not null,
  room       text not null default '',
  state      jsonb not null,
  version    integer not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
);
create index if not exists mind_entities_kind on mind_entities (kind, room);

-- 세계 모델의 이력: 누가, 무엇에서 무엇으로, 어떤 근거(원문 인용)로.
create table if not exists mind_changes (
  id             bigserial primary key,
  entity_id      text not null references mind_entities (id) on delete cascade,
  version        integer not null,
  actor          text not null,
  before         jsonb,
  after          jsonb not null,
  evidence       jsonb not null default '[]',
  confidence     real not null default 1,
  observation_id bigint,
  why            text not null default '',
  at             timestamptz not null
);
create index if not exists mind_changes_entity on mind_changes (entity_id, id);

-- 스킬이 걸어 둔 "이때 다시 보기".
create table if not exists mind_timers (
  id        bigserial primary key,
  skill     text not null,
  entity_id text not null,
  name      text not null,
  fire_at   timestamptz not null,
  fired_at  timestamptz,
  unique (skill, entity_id, name)
);
create index if not exists mind_timers_due on mind_timers (fire_at) where fired_at is null;

-- 의도와 그 결과. 관문이 무엇을 정했고 사람이 어떻게 답했는지가 다음 판단의
-- 근거(신뢰 단계)가 된다.
create table if not exists mind_intents (
  id         text primary key,
  entity_id  text not null default '',
  kind       text not null,
  status     text not null,
  body       jsonb not null,               -- model.Intent 전체
  created_at timestamptz not null
);
create index if not exists mind_intents_created on mind_intents (created_at desc);
create index if not exists mind_intents_entity on mind_intents (entity_id);

-- 감지기의 커서, 하루 한 번 도는 일의 날짜.
create table if not exists mind_cursors (
  name  text primary key,
  value bigint not null
);
