-- 이벤트. 모든 서비스가 "무슨 일이 있었나"를 events-svc 에 보내면 여기에 먼저
-- 쌓이고, 구독한 소비자(notify-svc, agent ...)에게 차례로 전달된다. events-svc
-- 는 판단하지 않는다 - 알릴지, 제안할지는 받은 쪽이 정한다.
create table if not exists events (
  id          bigserial primary key,
  source      text not null,                 -- 보낸 서비스
  type        text not null,                 -- '분류.세부', 예: spending.payment, assets.band
  subject     text not null default '',      -- 무엇에 관한 것인가: 거래 id, 종목 id ...
  data        jsonb not null default '{}',   -- 판단에 쓰는 원래 값
  -- 사람에게 보여 줄 문장(선택): {tier, level, title, body, url}. 알림을 바로 만들
  -- 수 있게 보내는 쪽이 붙인다. 없으면 받는 쪽이 data 로 만든다.
  notify      jsonb,
  dedupe_key  text unique,
  occurred_at timestamptz not null default now(),
  received_at timestamptz not null default now()
);
create index if not exists events_type on events (type, id);

-- 구독. 소비자가 시작할 때 스스로 등록한다: 받을 주소와 종류 패턴('*',
-- 'assets.*', 'memory.plan'). cursor 는 어디까지 전달했는지.
create table if not exists event_subscriptions (
  consumer   text primary key,
  url        text not null,
  types      text[] not null default '{*}',
  cursor     bigint not null default 0,
  failures   integer not null default 0,
  last_error text not null default '',
  last_ok_at timestamptz,
  updated_at timestamptz not null default now()
);
