-- 이벤트 격리함(gocommon/health 의 규칙). 소비자가 살아 있는데 한 이벤트만 다섯 번 받지
-- 못하면 그 이벤트를 여기로 빼고 커서를 넘긴다 - 그 하나 때문에 뒤의 알림·카드가 모두 멈춰
-- 있지 않게. 소비자가 꺼졌거나 토큰을 거절하는 것은 이벤트 탓이 아니라서 횟수를 세지 않고,
-- 커서는 그 자리에서 기다린다(failing_since 부터 상태 탭에 보인다).
--
-- 격리된 이벤트는 소비자가 다시 받기 시작할 때(실패 뒤 첫 전달, 재시작하며 다시 구독할 때)
-- 한 번 더 간다. 버리는 것은 사람이 한다(DELETE /held/{consumer}/{id}).
create table if not exists event_held (
  consumer   text not null references event_subscriptions (consumer) on delete cascade,
  event_id   bigint not null references events (id) on delete cascade,
  tries      integer not null,
  last_error text not null default '',
  held_at    timestamptz not null default now(),
  -- 다시 보낼 때. null 이면 다음 계기(소비자가 다시 받기 시작함, 사람이 다시 보내기)를 기다린다.
  next_try   timestamptz,
  primary key (consumer, event_id)
);

-- 실패가 언제부터 이어졌나(상태 탭의 "N분째 전하지 못함"), 그리고 지금 막힌 이벤트와 그
-- 이벤트가 받지 못한 횟수.
alter table event_subscriptions
  add column if not exists failing_since timestamptz,
  add column if not exists head_event    bigint,
  add column if not exists head_tries    integer not null default 0;

grant select, insert, update, delete on event_held to svc_events;
