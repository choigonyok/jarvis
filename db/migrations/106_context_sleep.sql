-- 수면: 밤 하나에 한 줄(context-svc POST /sleep, 생활의 수면 화면). 폰의 단축어나 Health Auto Export 가 보낸 수면 분석
-- 샘플이나 이미 합한 밤에서 만든다. 밤의 키는 깬 날(서울)이다 - 15일 밤에서 16일 아침까지는 16일.
--
-- 밤은 작아서 오래 둔다. 원본(보낸 본문)은 context_raw 에 kind = 'sleep' 으로 다른 원본처럼 CONTEXT_RAW_DAYS(기본 30일)만
-- 두고, 밤의 timeline(단계가 바뀐 자리들)도 원본이라 그날이 지나면 context-svc 가 비운다. 합계(잔 분·누운 분·깬 분·단계별
-- 분)와 낮잠은 남는다. 같은 밤이 다시 오면 이 줄을 바꿔 쓴다 - 둘이 되지 않는다.
create table if not exists context_sleep (
  night      date primary key,                 -- 깬 날
  bed_at     timestamptz not null,             -- 누운 때(모르면 잠든 때)
  wake_at    timestamptz not null,             -- 일어난 때(모르면 마지막으로 잠들어 있던 때)
  asleep_min integer not null check (asleep_min >= 0),
  in_bed_min integer not null check (in_bed_min >= 0),
  awake_min  integer not null default 0 check (awake_min >= 0),
  deep_min   integer check (deep_min >= 0),    -- 단계는 알려 준 출처(워치)만
  core_min   integer check (core_min >= 0),
  rem_min    integer check (rem_min >= 0),
  bed_only   boolean not null default false,   -- 잠은 모르고 침대에 있던 때만 왔다(워치 없는 아이폰): asleep_min 이 그 시간
  source     text not null default '',
  naps       jsonb not null default '[]'::jsonb,  -- 그날의 다른 잠 [{bedAt, wakeAt, asleepMinutes}]
  timeline   jsonb,                            -- [{from, to, stage}] 누운 때부터의 분. 원본과 같은 날수만
  updated_at timestamptz not null default now(),
  check (wake_at > bed_at)
);
create index if not exists context_sleep_wake on context_sleep (wake_at) where timeline is not null;

grant select, insert, update, delete on context_sleep to svc_context;
