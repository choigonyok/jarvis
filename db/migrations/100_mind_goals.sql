-- 목표(mind-svc internal/goals). 매일·요일마다·주 n회·월 n회 하려는 일과, 날마다 했다/안 했다.
-- 운영자와 손님 각자의 것이고(body 의 who), 달성률·연속·이번 주가 어떤지는 기록에서 그때그때 계산한다.
create table if not exists mind_goals (
  id         text primary key,
  status     text not null default 'active' check (status in ('active', 'paused', 'ended')),
  body       jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists mind_goal_marks (
  goal_id text not null references mind_goals (id) on delete cascade,
  day     date not null,
  mark    text not null check (mark in ('done', 'missed')),
  note    text not null default '',
  by_whom text not null default '',
  at      timestamptz not null default now(),
  primary key (goal_id, day)
);

grant select, insert, update, delete on mind_goals, mind_goal_marks to svc_mind;
