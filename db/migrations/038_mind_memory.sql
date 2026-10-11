-- mind 의 근거 장부와 사람 ID 표. 해석기가 원문 인용으로 확인한 "기억할 사실"은
-- 여기 먼저 쓰이고(outbox), 기억 서비스(memory-svc /facts)가 받으면 그 판단 -
-- 어느 노드에 묶였는지, 이미 알던 사실인지, 어떤 사실을 끝냈는지 - 이 같은
-- 행에 돌아온다.
create table if not exists mind_facts (
  id         text primary key,             -- mf_…, memory-svc 의 ref
  room       text not null default '',
  body       jsonb not null,               -- model.Fact 전체
  status     text not null,                -- pending | stored | duplicate | failed
  next_try   timestamptz not null default now(),
  created_at timestamptz not null
);
create index if not exists mind_facts_due on mind_facts (next_try) where status = 'pending';
create index if not exists mind_facts_created on mind_facts (created_at desc);

-- 대화 속 이름 → 기억 그래프의 노드. 기억이 "이 지수는 그 지수"라고 답한 것을
-- 적어 두고, 다음 사실부터는 이름 대신 노드로 보낸다.
create table if not exists mind_aliases (
  name  text primary key,
  kind  text not null default '',
  node  text not null,
  seen  integer not null default 1,
  at    timestamptz not null
);
