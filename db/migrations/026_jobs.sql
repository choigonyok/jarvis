-- 중고나라 전용 테이블은 공용 작업으로 바뀌었다. 실제 글은 없었으므로 그대로 지운다.
drop table if exists market_tasks;
drop table if exists market_listings;
drop table if exists market_state;

-- 작업: 대화에서 시킨 "알아서 해 둘 일". 에이전트가 백그라운드에서 브라우저로 하고,
-- 매 실행 끝에 다음에 언제 다시 볼지(next_at), 승인을 기다릴지(waiting), 끝낼지를
-- 스스로 정한다. 실행과 실행 사이의 기억은 job_memory·job_records·job_events 에 있다.
create table if not exists jobs (
  id               bigserial primary key,
  title            text not null,
  goal             text not null,
  instructions     text not null default '',
  -- 이 작업이 로그인해서 쓰는 사이트(호스트). 그 사이트가 로그인 대기면 작업도 쉰다.
  sites            text[] not null default '{}',
  -- 대화에 첨부한 사진(에이전트 /uploads 의 이름).
  photos           text[] not null default '{}',
  -- active 예정대로 / waiting 카드 대기 / paused 사람이 멈춤 / done 끝남
  -- cancelled 그만둠 / failed 세 번 연달아 실패
  state            text not null default 'active'
                   check (state in ('active', 'waiting', 'paused', 'done', 'cancelled', 'failed')),
  next_at          timestamptz,
  next_reason      text not null default '',
  waiting_card     text not null default '',
  -- 한 줄 현황. 실행이 report 로 갱신한다.
  summary          text not null default '',
  attempts         int not null default 0,
  last_run_at      timestamptz,
  finished_at      timestamptz,
  photos_purged_at timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);
create index if not exists jobs_due_idx on jobs (next_at) where state = 'active';

create table if not exists job_runs (
  id         bigserial primary key,
  job_id     bigint not null references jobs(id) on delete cascade,
  started_at timestamptz not null default now(),
  ended_at   timestamptz,
  -- ok / yielded(대화에 양보) / error
  outcome    text,
  -- 이번 실행이 다음 할 일(schedule·wait·finish)을 정했는지. 안 정하고 끝나면 실패로 센다.
  decided    boolean not null default false,
  note       text not null default ''
);
create index if not exists job_runs_job_idx on job_runs (job_id, id desc);

-- 진행 기록. 탭이 이것을 시간순으로 보여 준다.
create table if not exists job_events (
  id      bigserial primary key,
  job_id  bigint not null references jobs(id) on delete cascade,
  run_id  bigint references job_runs(id) on delete set null,
  kind    text not null,
  text    text not null,
  at      timestamptz not null default now()
);
create index if not exists job_events_job_idx on job_events (job_id, id desc);

-- 실행이 다음 실행에 남기는 메모(이미 올린 글 번호, 알아낸 절차 등).
create table if not exists job_memory (
  job_id     bigint not null references jobs(id) on delete cascade,
  key        text not null,
  value      text not null,
  updated_at timestamptz not null default now(),
  primary key (job_id, key)
);

-- 작업이 지켜보는 대상들의 현황(판매 글, 택배, 매물…). key 는 외부 id.
create table if not exists job_records (
  job_id     bigint not null references jobs(id) on delete cascade,
  key        text not null,
  title      text not null,
  status     text not null default '',
  amount     bigint,
  metrics    jsonb not null default '{}',
  image      text not null default '',
  url        text not null default '',
  note       text not null default '',
  updated_at timestamptz not null default now(),
  primary key (job_id, key)
);

-- 사람이 작업에 건네는 것: 추가 지시, 카드에 대한 답. 다음 실행이 읽고 소비한다.
create table if not exists job_inbox (
  id           bigserial primary key,
  job_id       bigint not null references jobs(id) on delete cascade,
  kind         text not null check (kind in ('instruction', 'answer')),
  text         text not null,
  consumed_run bigint references job_runs(id) on delete set null,
  at           timestamptz not null default now()
);
create index if not exists job_inbox_open_idx on job_inbox (job_id) where consumed_run is null;

-- 사이트 로그인 상태. 하나가 막히면 그 사이트를 쓰는 작업이 모두 쉰다.
create table if not exists job_sites (
  site           text primary key,
  login_required boolean not null default false,
  note           text not null default '',
  updated_at     timestamptz not null default now()
);
