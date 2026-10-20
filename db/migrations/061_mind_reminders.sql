-- 때 맞춘 알림(mind-svc internal/moments). 운영자가 "알려줘"라고 한 것을 정한 시각이나
-- 집·회사에 도착하거나 나갈 때 한 번 폰으로 알린다. 대화(에이전트의 리마인드 도구)가
-- mind 의 /api/reminders 로 만들고 고치며, 자비스가 그 순간까지 미뤄 둔 자기 알림(퇴근 뒤
-- 집에서 볼 리밸런싱)도 여기서 기다린다. 알림 하나가 body 한 덩어리(JSON)이고, 찾는 데 쓰는
-- 칸만 따로 둔다.
create table if not exists mind_reminders (
  id         text primary key,
  status     text not null default 'waiting' check (status in ('waiting', 'sent', 'cancelled', 'expired')),
  -- 같은 주제는 하나만 기다린다(rebalance). 빈 값이면 주제 없음.
  topic      text not null default '',
  body       jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists mind_reminders_waiting on mind_reminders (status) where status = 'waiting';
create index if not exists mind_reminders_updated on mind_reminders (updated_at desc);

grant select, insert, update, delete on mind_reminders to svc_mind;
