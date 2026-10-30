-- 입구 하나(to-be 4a). 지금까지는 서비스가 event_outbox 에 쓰면 events-svc 가 events 로 옮기고 구독자에게 HTTP 로
-- 밀어 줬다. 이제 outbox 는 input 스키마의 받은편지함이고, input-svc 만 꺼내서 봉투를 검증해 기록으로 남긴다.
-- 기록을 저장하는 일과 소비자별 배달 행을 만드는 일은 한 트랜잭션이다(저장+발행).
--
-- 큐는 프로세스가 아니라 이 표들과 gocommon/queue 다. 소비자는 자기 배달 행을 SKIP LOCKED 로 집고, 처리하면 지우고,
-- 지워지지 않은 가장 낮은 행의 바로 아래까지 cursor 를 올린다(하한선). 오프셋 하나로는 병렬 워커가 앞 행을 못
-- 끝낸 채 뒤 행을 끝낸 상태를 적을 수 없어서, 소비자별 행으로 둔다. 순서는 subject 단위로 지킨다.
--
-- 손님 것은 전용 표로 간다(guest_*). 손님 역할 svc_guest 는 그 표만 읽고, 자기 배달 행만 집는다.

-- 받은편지함. 있는 행과 권한을 그대로 들고 옮긴다. 옛 이름의 뷰는 재시작하는 동안만 쓰이고 079 에서 지운다.
alter table event_outbox set schema input;
alter table input.event_outbox rename to outbox;
alter sequence input.event_outbox_id_seq rename to outbox_id_seq;
create view public.event_outbox as select id, payload, created_at from input.outbox;

create or replace function input.outbox_wake() returns trigger language plpgsql as $$
begin
  perform pg_notify('input_outbox', '');
  return null;
end $$;
create trigger outbox_wake after insert on input.outbox
  for each statement execute function input.outbox_wake();

-- 봉투가 잘못돼 기록이 되지 못한 것. 버리지 않고 남겨 상태 탭이 센다.
create table input.rejected (
  id          bigint primary key,      -- outbox 의 id
  payload     jsonb not null,
  why         text not null,
  rejected_at timestamptz not null default now()
);

-- 손님의 기록. 모양은 input.records 와 같고 id 도 같은 번호표를 쓴다 - 둘을 합쳐 읽어도 순서가 하나다.
create table input.guest_records (like input.records including defaults including constraints);
alter table input.guest_records add primary key (received_at, id);
create index guest_records_kind on input.guest_records (kind, received_at desc);
create trigger guest_records_guard before update or delete on input.guest_records
  for each row execute function input.records_guard();

create table input.consumers (
  name          text primary key,
  kinds         text[] not null,           -- 'mind.card', 'remind.*', '*'
  cursor        bigint not null default 0, -- 이 번호까지는 처리됨(격리된 것 제외)
  high          bigint not null default 0, -- 처리한 가장 높은 번호. 기다리는 행이 없으면 커서가 여기까지 오른다
  failures      integer not null default 0,
  last_error    text not null default '',
  failing_since timestamptz,
  last_ok_at    timestamptz,
  registered_at timestamptz not null default now()
);

create table input.deliveries (
  consumer    text not null references input.consumers (name) on delete cascade,
  id          bigint not null,                -- 기록의 id
  at          timestamptz not null,           -- 기록의 received_at (파티션 키)
  subject     text not null default '',       -- 순서를 지키는 단위
  tries       integer not null default 0,     -- 제 잘못으로 실패한 횟수
  not_before  timestamptz default now(),      -- 이때부터 집을 수 있다. 비면 격리되어 기다리는 중
  lease_until timestamptz,                    -- 워커가 쥐고 있는 기한. 죽은 워커의 행은 지나면 다시 집힌다
  held_at     timestamptz,                    -- 격리함에 들어간 때
  last_error  text not null default '',
  primary key (consumer, id)
);
create index deliveries_ready on input.deliveries (consumer, id) where not_before is not null;
create index deliveries_subject on input.deliveries (consumer, subject, id) where held_at is null;
create index deliveries_record on input.deliveries (id);

create table input.guest_consumers (like input.consumers including defaults including constraints);
alter table input.guest_consumers add primary key (name);
create table input.guest_deliveries (like input.deliveries including defaults including constraints);
alter table input.guest_deliveries add primary key (consumer, id);
alter table input.guest_deliveries add foreign key (consumer) references input.guest_consumers (name) on delete cascade;
create index guest_deliveries_ready on input.guest_deliveries (consumer, id) where not_before is not null;
create index guest_deliveries_subject on input.guest_deliveries (consumer, subject, id) where held_at is null;
create index guest_deliveries_record on input.guest_deliveries (id);

-- 기록을 남기는 길은 여전히 함수 하나다. 이제 input-svc 만 부른다. 처음 보는 (source, source_key) 면 번호와 받은
-- 시각을, 이미 받은 것이면 빈 값을 돌려준다. 받은 시각은 한 묶음 안에서도 번호 순으로 오르게 벽시계를 쓴다.
drop function input.append(text, text, text, text, text, text, timestamptz, text, integer, text, text, jsonb);
create function input.append(
  p_guest boolean, p_kind text, p_source text, p_source_key text, p_actor text, p_channel text, p_subject text,
  p_occurred_at timestamptz, p_tier text, p_schema_version integer,
  p_trace_id text, p_causation_id text, p_payload jsonb,
  out r_id bigint, out r_at timestamptz
) language plpgsql security definer set search_path = pg_catalog, input as $$
begin
  insert into input.record_keys (source, source_key) values (p_source, p_source_key)
    on conflict do nothing;
  if not found then
    return;
  end if;
  r_id := nextval('input.records_id_seq');
  r_at := clock_timestamp();
  if p_guest then
    insert into input.guest_records
      (id, kind, source, source_key, actor, channel, subject, occurred_at, received_at, tier, schema_version, trace_id, causation_id, payload)
      values
      (r_id, p_kind, p_source, p_source_key, p_actor, p_channel, p_subject, p_occurred_at, r_at, p_tier, p_schema_version, p_trace_id, p_causation_id, p_payload);
  else
    insert into input.records
      (id, kind, source, source_key, actor, channel, subject, occurred_at, received_at, tier, schema_version, trace_id, causation_id, payload)
      values
      (r_id, p_kind, p_source, p_source_key, p_actor, p_channel, p_subject, p_occurred_at, r_at, p_tier, p_schema_version, p_trace_id, p_causation_id, p_payload);
  end if;
end $$;
revoke all on function input.append(boolean, text, text, text, text, text, text, timestamptz, text, integer, text, text, jsonb) from public;

-- 보존기간이 지난 원본층을 지운다. 아직 누가 받지 못한 기록은 남긴다. 지속층은 건드리지 않는다.
create function input.prune_raw(p_before timestamptz) returns bigint
language plpgsql security definer set search_path = pg_catalog, input as $$
declare
  n bigint := 0;
  m bigint := 0;
begin
  with gone as (
    delete from input.records r
     where r.tier = 'raw' and r.received_at < p_before
       and not exists (select 1 from input.deliveries d where d.id = r.id)
    returning r.source, r.source_key
  )
  delete from input.record_keys k using gone g where k.source = g.source and k.source_key = g.source_key;
  get diagnostics n = row_count;
  with gone as (
    delete from input.guest_records r
     where r.tier = 'raw' and r.received_at < p_before
       and not exists (select 1 from input.guest_deliveries d where d.id = r.id)
    returning r.source, r.source_key
  )
  delete from input.record_keys k using gone g where k.source = g.source and k.source_key = g.source_key;
  get diagnostics m = row_count;
  return n + m;
end $$;
revoke all on function input.prune_raw(timestamptz) from public;

-- 앞으로 p_months 달 치 파티션이 있게 한다. 혹시 밀려도 기본 파티션이 받아 기록을 잃지 않는다.
create function input.ensure_partitions(p_months integer) returns integer
language plpgsql security definer set search_path = pg_catalog, input as $$
declare
  m date := date_trunc('month', now())::date;
  made integer := 0;
  name text;
begin
  for i in 0 .. p_months loop
    name := 'records_' || to_char(m, 'YYYY_MM');
    if to_regclass('input.' || name) is null then
      execute format('create table input.%I partition of input.records for values from (%L) to (%L)',
        name, m, (m + interval '1 month')::date);
      made := made + 1;
    end if;
    m := (m + interval '1 month')::date;
  end loop;
  return made;
end $$;
revoke all on function input.ensure_partitions(integer) from public;

-- 작업의 진행도 이제 outbox 로 간다. 받는 쪽이 input-svc 하나가 되도록.
create or replace function input.job_event_record() returns trigger
language plpgsql security definer set search_path = pg_catalog, public, input as $$
declare
  owner_job boolean;
begin
  select who = 'owner' into owner_job from jobs where id = new.job_id;
  if owner_job is not true then
    return null;
  end if;
  insert into input.outbox (payload) values (jsonb_build_object(
    'kind', 'job.' || new.kind, 'source', 'jobs', 'source_key', 'job_event:' || new.id,
    'actor', 'job:' || new.job_id, 'channel', 'job', 'subject', 'job:' || new.job_id,
    'occurred_at', new.at, 'tier', 'raw', 'schema_version', 1, 'trace_id', 'job:' || new.job_id,
    'payload', jsonb_build_object('job_id', new.job_id, 'run_id', new.run_id, 'kind', new.kind, 'text', new.text)));
  return null;
exception when others then
  raise warning '작업 기록을 남기지 못했습니다 (job_event %): %', new.id, sqlerrm;
  return null;
end $$;

-- 역할. 비밀번호는 scripts/db-roles 가 넣는다.
do $$
declare r text;
begin
  foreach r in array array['svc_input', 'svc_guest'] loop
    if not exists (select 1 from pg_roles where rolname = r) then
      execute format('create role %I login', r);
    end if;
    execute format('grant connect on database %I to %I', current_database(), r);
  end loop;
end $$;

-- 내는 쪽: 받은편지함에 쓰기만.
grant usage on schema input to svc_agent, svc_assets, svc_calendar, svc_context, svc_mind, svc_spending, svc_status;
grant insert on input.outbox to svc_agent, svc_assets, svc_calendar, svc_context, svc_mind, svc_spending, svc_status;
grant usage on sequence input.outbox_id_seq to svc_agent, svc_assets, svc_calendar, svc_context, svc_mind, svc_spending, svc_status;
grant insert on public.event_outbox to svc_agent, svc_assets, svc_calendar, svc_context, svc_mind, svc_spending, svc_status;

-- input-svc: 받은편지함을 비우고 기록과 배달을 만든다. 기록 표에 직접 쓰는 권한은 여기도 없다.
grant usage on schema input to svc_input;
grant select, update, delete on input.outbox to svc_input; -- update: FOR UPDATE 로 집으려면 필요하다
grant select, insert on input.rejected to svc_input;
grant select on input.records, input.guest_records to svc_input;
grant select, insert, update, delete on input.deliveries, input.guest_deliveries to svc_input;
grant select, insert, update on input.consumers, input.guest_consumers to svc_input;
grant execute on function input.append(boolean, text, text, text, text, text, text, timestamptz, text, integer, text, text, jsonb) to svc_input;
grant execute on function input.prune_raw(timestamptz), input.ensure_partitions(integer) to svc_input;

-- 읽는 쪽: 기록을 읽고, 자기 배달 행을 처리한다.
grant usage on schema input to svc_notify;
grant select on input.records to svc_agent, svc_mind, svc_notify;
grant select, update, delete on input.deliveries to svc_agent, svc_mind, svc_notify;
grant select, insert, update on input.consumers to svc_agent, svc_mind, svc_notify;
grant select on input.guest_records to svc_mind, svc_notify;
grant select, update, delete on input.guest_deliveries to svc_mind, svc_notify;
grant select, insert, update on input.guest_consumers to svc_mind, svc_notify;

-- 손님: 손님 표만, 그중에서도 자기 몫만.
grant usage on schema input to svc_guest;
grant select on input.guest_records to svc_guest;
grant select, update, delete on input.guest_deliveries to svc_guest;
grant select, insert, update on input.guest_consumers to svc_guest;
alter table input.guest_consumers enable row level security;
alter table input.guest_deliveries enable row level security;
create policy guest_own on input.guest_consumers to svc_guest using (name = 'agent-guest') with check (name = 'agent-guest');
create policy guest_own on input.guest_deliveries to svc_guest using (consumer = 'agent-guest');
create policy services on input.guest_consumers to svc_input, svc_mind, svc_notify using (true) with check (true);
create policy services on input.guest_deliveries to svc_input, svc_mind, svc_notify using (true) with check (true);

-- 지금까지의 이벤트를 기록으로 옮긴다. 재생이 여기서 시작할 수 있게. 번호는 새로 받고, 받은 시각은 그때 그대로다.
-- 손님에게 가는 것은 손님 표로. 지금 모양의 data 는 전부 객체이고, 알림 문구는 payload 의 notify 로 들어간다.
create function pg_temp.matches(patterns text[], kind text) returns boolean language sql immutable as $$
  select exists (select 1 from unnest(patterns) p
    where p = '*' or p = kind or (p like '%.*' and kind like left(p, -1) || '%'))
$$;

create temporary table moved on commit drop as
  select e.id as event_id, nextval('input.records_id_seq') as record_id, e.source, e.type,
         coalesce(e.dedupe_key, 'events:' || e.id) as source_key, e.subject, e.occurred_at, e.received_at,
         coalesce(e.notify->>'audience', '') = 'guest' as guest,
         e.data || case when e.notify is null then '{}'::jsonb else jsonb_build_object('notify', e.notify) end as payload
    from (select * from events order by id) e;

insert into input.record_keys (source, source_key)
  select source, source_key from moved on conflict do nothing;

insert into input.records
  (id, kind, source, source_key, actor, channel, subject, occurred_at, received_at, tier, schema_version, trace_id, causation_id, payload)
  select record_id, type, source, source_key, source, '', subject, occurred_at, received_at, 'raw', 1, '', '', payload
    from moved where not guest order by record_id;
insert into input.guest_records
  (id, kind, source, source_key, actor, channel, subject, occurred_at, received_at, tier, schema_version, trace_id, causation_id, payload)
  select record_id, type, source, source_key, 'guest', '', subject, occurred_at, received_at, 'raw', 1, '', '', payload
    from moved where guest order by record_id;

-- 구독을 옮긴다. 다음 부팅에서 소비자가 자기 kinds 를 다시 적는다. 손님 표에는 전부 받는 notify 와, 손님 위치를
-- 읽는 mind, 손님 에이전트가 붙는다. 이름이 겹치지 않게 손님 쪽은 -guest 로 끝난다.
insert into input.consumers (name, kinds)
  select consumer, types from event_subscriptions where not guest;
insert into input.guest_consumers (name, kinds) values
  ('agent-guest', array['remind.*']),
  ('mind-guest', array['location.guest.*']),
  ('notify-guest', array['*']);

-- 아직 못 받은 것은 그대로 받게 한다. 격리되어 있던 것은 격리함으로.
insert into input.deliveries (consumer, id, at, subject)
  select s.consumer, m.record_id, m.received_at, m.subject
    from event_subscriptions s join moved m on m.event_id > s.cursor and not m.guest and pg_temp.matches(s.types, m.type)
   where not s.guest;
insert into input.guest_deliveries (consumer, id, at, subject)
  select c.name, m.record_id, m.received_at, m.subject
    from input.guest_consumers c
    join event_subscriptions s on s.consumer = case c.name when 'agent-guest' then 'agent-guest' when 'mind-guest' then 'mind' else 'notify' end
    join moved m on m.event_id > s.cursor and m.guest and pg_temp.matches(c.kinds, m.type);
insert into input.deliveries (consumer, id, at, subject, tries, not_before, held_at, last_error)
  select h.consumer, m.record_id, m.received_at, m.subject, h.tries, h.next_try, h.held_at, h.last_error
    from event_held h join moved m on m.event_id = h.event_id and not m.guest
    join input.consumers c on c.name = h.consumer
  on conflict do nothing;
