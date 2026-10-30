-- 기록(Record). to-be 구조의 입구: 일어난 일은 전부 이 꼴로 남고, 재생은 이 표를 SELECT 하는 것이다.
-- 0단계에서는 재생할 것을 쌓기 시작하는 데까지만 쓴다 - 결제 가드의 질의·답(에이전트), 결제 카드와 답,
-- 작업의 진행(job_events 트리거). 판단은 아직 아무도 여기서 읽지 않는다.
--
-- 쓰는 길은 input.append 하나다. 표에 직접 쓰는 권한은 어느 역할에도 주지 않는다 - 받은 시각(received_at)을
-- 서버가 찍고, 멱등 키를 한 곳에서 보고, 나중에 input-svc 가 유일한 writer 가 될 때 EXECUTE 권한만 옮기면 되게.
create schema if not exists input;

create sequence if not exists input.records_id_seq;

create table if not exists input.records (
  id             bigint not null default nextval('input.records_id_seq'),
  kind           text not null,                  -- 'guard.query', 'intent.answer', 'job.step' ...
  source         text not null,                  -- 낸 곳: agent | guard | jobs
  source_key     text not null,                  -- 낸 곳 안에서의 멱등 키
  actor          text not null,                  -- 누가 한 일인가: 'operator', 'job:12', 'guard', 'web'
  channel        text not null default '',       -- 어느 길로 왔나: 'console', 'browser', 'job' (화자 인증과 따로)
  subject        text not null default '',       -- 무엇에 관한 것인가. 순서는 이 단위로 지킨다
  occurred_at    timestamptz not null,           -- 세상에서 일어난 시각
  received_at    timestamptz not null default now(), -- 우리가 받은 시각. 재생은 이 순서
  tier           text not null check (tier in ('raw', 'durable')), -- raw 는 보존기간 뒤 지울 수 있고, durable 은 불변
  schema_version integer not null default 1,     -- payload 의 kind 별 버전
  trace_id       text not null default '',       -- 한 흐름을 잇는 끈: 'job:12', 'chat'
  causation_id   text not null default '',       -- 이 기록을 낳은 기록의 source_key
  payload        jsonb not null default '{}'::jsonb,
  primary key (received_at, id)
) partition by range (received_at);

create index if not exists records_kind on input.records (kind, received_at desc);
create index if not exists records_trace on input.records (trace_id, received_at) where trace_id <> '';

-- 달마다 나눈다. 2년 치를 미리 만들어 두어 관리 작업 없이 돌고(그 뒤는 input-svc 가 만든다), 혹시 넘겨도
-- 기본 파티션이 받아 기록을 잃지 않는다.
create table if not exists input.records_default partition of input.records default;
do $$
declare m date := date '2026-10-01';
begin
  while m < date '2029-01-01' loop
    execute format(
      'create table if not exists input.records_%s partition of input.records for values from (%L) to (%L)',
      to_char(m, 'YYYY_MM'), m, (m + interval '1 month')::date);
    m := (m + interval '1 month')::date;
  end loop;
end $$;

-- 멱등 키는 파티션을 넘어 하나여야 한다. 파티션 표의 유일 제약은 파티션 키를 포함해야 해서 따로 둔다.
create table if not exists input.record_keys (
  source     text not null,
  source_key text not null,
  primary key (source, source_key)
);

-- 고치지 않는다. 지우는 것은 raw 층만(보존기간). durable 을 사람 단위로 지워야 할 때는 트리거를 껐다 켠다.
create or replace function input.records_guard() returns trigger language plpgsql as $$
begin
  if tg_op = 'UPDATE' then
    raise exception '기록은 고치지 않습니다. 정정은 새 기록으로 남기세요.';
  end if;
  if old.tier = 'durable' then
    raise exception '지속층 기록은 지우지 않습니다 (id %).', old.id;
  end if;
  return old;
end $$;
drop trigger if exists records_guard on input.records;
create trigger records_guard before update or delete on input.records
  for each row execute function input.records_guard();

-- 기록을 하나 남긴다. 처음 보는 (source, source_key) 면 참, 이미 받은 것이면 거짓.
create or replace function input.append(
  p_kind text, p_source text, p_source_key text, p_actor text, p_channel text, p_subject text,
  p_occurred_at timestamptz, p_tier text, p_schema_version integer,
  p_trace_id text, p_causation_id text, p_payload jsonb
) returns boolean
language plpgsql security definer set search_path = pg_catalog, input as $$
begin
  insert into input.record_keys (source, source_key) values (p_source, p_source_key)
    on conflict do nothing;
  if not found then
    return false;
  end if;
  insert into input.records
    (kind, source, source_key, actor, channel, subject, occurred_at, tier, schema_version, trace_id, causation_id, payload)
    values
    (p_kind, p_source, p_source_key, p_actor, p_channel, p_subject, p_occurred_at, p_tier, p_schema_version, p_trace_id, p_causation_id, p_payload);
  return true;
end $$;
revoke all on function input.append(text, text, text, text, text, text, timestamptz, text, integer, text, text, jsonb) from public;

-- 에이전트가 쓴다(가드의 질의·답, 결제 카드, 카드의 답). 읽는 역할은 아직 없다 - 하네스는 소유자로 내려받는다.
grant usage on schema input to svc_agent;
grant execute on function input.append(text, text, text, text, text, text, timestamptz, text, integer, text, text, jsonb) to svc_agent;

-- 작업의 진행은 jobs-svc 를 고치지 않고 트리거로 남긴다: job_events 의 쓰기는 두 곳이고 한 줄도 놓치면 안 되며,
-- jobs-svc 는 전환 4e 에서 녹아 없어진다. 손님의 작업은 넣지 않는다(손님 기록은 전용 표로, 그 표는 4단계).
-- 기록을 남기지 못해도 작업의 진행은 막지 않는다: 오류는 경고로만.
create or replace function input.job_event_record() returns trigger
language plpgsql security definer set search_path = pg_catalog, public, input as $$
declare
  owner_job boolean;
begin
  select who = 'owner' into owner_job from jobs where id = new.job_id;
  if owner_job is not true then
    return null;
  end if;
  perform input.append(
    'job.' || new.kind, 'jobs', 'job_event:' || new.id, 'job:' || new.job_id, 'job', 'job:' || new.job_id,
    new.at, 'raw', 1, 'job:' || new.job_id, '',
    jsonb_build_object('job_id', new.job_id, 'run_id', new.run_id, 'kind', new.kind, 'text', new.text));
  return null;
exception when others then
  raise warning '작업 기록을 남기지 못했습니다 (job_event %): %', new.id, sqlerrm;
  return null;
end $$;
drop trigger if exists job_events_record on job_events;
create trigger job_events_record after insert on job_events
  for each row execute function input.job_event_record();

-- 이미 있는 작업 기록을 옮긴다. 10/29 LP 사고의 작업 쪽 절반이 여기서 재생할 수 있게 된다. 받은 시각은 그때다.
with moved as (
  insert into input.record_keys (source, source_key)
  select 'jobs', 'job_event:' || e.id
    from job_events e join jobs j on j.id = e.job_id
   where j.who = 'owner'
  on conflict do nothing
  returning source_key
)
insert into input.records
  (kind, source, source_key, actor, channel, subject, occurred_at, received_at, tier, schema_version, trace_id, causation_id, payload)
select 'job.' || e.kind, 'jobs', 'job_event:' || e.id, 'job:' || e.job_id, 'job', 'job:' || e.job_id,
       e.at, e.at, 'raw', 1, 'job:' || e.job_id, '',
       jsonb_build_object('job_id', e.job_id, 'run_id', e.run_id, 'kind', e.kind, 'text', e.text)
  from job_events e
  join moved m on m.source_key = 'job_event:' || e.id
 order by e.id;
