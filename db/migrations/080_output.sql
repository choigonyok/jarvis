-- output (to-be 4b). output-svc 가 output-svc 가 된다: 스트림의 기록을 직접 읽지 않고 mind 가 낸 효과(effects)를 읽어
-- 푸시·알림함으로 내보낸다. 알림함 표는 output 스키마로, 역할은 svc_output 으로 옮긴다.
--
-- effects 는 mind 가 쓰는 토픽이다(input 을 거치지 않는다 - 기록은 세상에서 온 것이고, 효과는 mind 가 정한 것이다).
-- 다른 서비스가 붙인 알림 문구는 아직 그 서비스에 있어서 mind 가 그대로 넘긴다(mind-relay). 규칙은 5단계에서 레지스트리로 간다.
--
-- 예외 하나: 상태 알림과 결재 대기(status.*, approval.*)는 output 이 기록에서 바로 읽는다(output-status). mind 가 죽었다는
-- 알림이 mind 를 지나갈 수는 없고, 결제 승인 알림이 mind 재배포에 묶이면 안 된다. 판단이 아니라 전달이다.

create schema output;
alter role svc_notify rename to svc_output;
grant usage on schema output to svc_output;

alter table notifications set schema output;
alter table push_subscriptions set schema output;
alter table notify_audience_settings set schema output;
alter table output.notify_audience_settings rename to settings;
drop table notify_settings;

-- 효과. 모양은 기록과 같아서 같은 소비 라이브러리로 읽는다.
create table mind.effects (
  id             bigserial primary key,
  kind           text not null,
  source         text not null,
  source_key     text not null,
  actor          text not null,
  channel        text not null default '',
  subject        text not null default '',
  occurred_at    timestamptz not null,
  received_at    timestamptz not null default clock_timestamp(),
  tier           text not null default 'raw' check (tier in ('raw', 'durable')),
  schema_version integer not null default 1,
  trace_id       text not null default '',
  causation_id   text not null default '',
  payload        jsonb not null default '{}'::jsonb,
  unique (source, source_key)
);
create index effects_received on mind.effects (received_at);

create table mind.effect_consumers (like input.consumers including defaults including constraints);
alter table mind.effect_consumers add primary key (name);
create table mind.effect_deliveries (like input.deliveries including defaults including constraints);
alter table mind.effect_deliveries add primary key (consumer, id);
alter table mind.effect_deliveries add foreign key (consumer) references mind.effect_consumers (name) on delete cascade;
create index effect_deliveries_ready on mind.effect_deliveries (consumer, id) where not_before is not null;
create index effect_deliveries_subject on mind.effect_deliveries (consumer, subject, id) where held_at is null;
create index effect_deliveries_record on mind.effect_deliveries (id);

-- 손님의 효과. 손님 역할은 여기만 본다.
create table mind.guest_effects (like mind.effects including defaults including constraints);
alter table mind.guest_effects alter column id set default nextval('mind.effects_id_seq');
alter table mind.guest_effects add primary key (id);
alter table mind.guest_effects add unique (source, source_key);
create index guest_effects_received on mind.guest_effects (received_at);
create table mind.guest_effect_consumers (like input.consumers including defaults including constraints);
alter table mind.guest_effect_consumers add primary key (name);
create table mind.guest_effect_deliveries (like input.deliveries including defaults including constraints);
alter table mind.guest_effect_deliveries add primary key (consumer, id);
alter table mind.guest_effect_deliveries add foreign key (consumer) references mind.guest_effect_consumers (name) on delete cascade;
create index guest_effect_deliveries_ready on mind.guest_effect_deliveries (consumer, id) where not_before is not null;
create index guest_effect_deliveries_subject on mind.guest_effect_deliveries (consumer, subject, id) where held_at is null;
create index guest_effect_deliveries_record on mind.guest_effect_deliveries (id);

-- mind: 효과를 낸다. 소비자가 누구인지 읽고, 배달 행을 만든다.
grant select, insert on mind.effects, mind.guest_effects to svc_mind;
grant usage on sequence mind.effects_id_seq to svc_mind;
grant select, insert on mind.effect_deliveries, mind.guest_effect_deliveries to svc_mind;
grant select on mind.effect_consumers, mind.guest_effect_consumers to svc_mind;
grant delete on mind.effects, mind.guest_effects to svc_mind;

-- 에이전트: 카드와 리마인드를 효과에서 읽는다(6단계에서 output 으로 옮길 때까지).
grant usage on schema mind to svc_agent;
grant select on mind.effects to svc_agent;
grant select, update, delete on mind.effect_deliveries to svc_agent;
grant select, insert, update on mind.effect_consumers to svc_agent;

-- output: 효과 둘 다, 그리고 상태·결재 대기는 기록에서. 낸 결과는 받은편지함으로.
grant usage on schema mind to svc_output;
grant select on mind.effects, mind.guest_effects to svc_output;
grant select, update, delete on mind.effect_deliveries, mind.guest_effect_deliveries to svc_output;
grant select, insert, update on mind.effect_consumers, mind.guest_effect_consumers to svc_output;
grant insert on input.outbox to svc_output;
grant usage on sequence input.outbox_id_seq to svc_output;
revoke all on input.guest_records, input.guest_deliveries, input.guest_consumers from svc_output;
drop policy services on input.guest_consumers;
drop policy services on input.guest_deliveries;
create policy services on input.guest_consumers to svc_input, svc_mind using (true) with check (true);
create policy services on input.guest_deliveries to svc_input, svc_mind using (true) with check (true);

-- 손님: 손님 효과에서 자기 몫만. 손님 기록은 이제 읽지 않는다.
grant usage on schema mind to svc_guest;
grant select on mind.guest_effects to svc_guest;
grant select, update, delete on mind.guest_effect_deliveries to svc_guest;
grant select, insert, update on mind.guest_effect_consumers to svc_guest;
drop policy guest_own on input.guest_consumers;
drop policy guest_own on input.guest_deliveries;
revoke all on input.guest_records, input.guest_deliveries, input.guest_consumers from svc_guest;
revoke usage on schema input from svc_guest;
alter table mind.guest_effect_consumers enable row level security;
alter table mind.guest_effect_deliveries enable row level security;
create policy guest_own on mind.guest_effect_consumers to svc_guest using (name = 'agent-guest') with check (name = 'agent-guest');
create policy guest_own on mind.guest_effect_deliveries to svc_guest using (consumer = 'agent-guest');
create policy services on mind.guest_effect_consumers to svc_input, svc_mind, svc_output using (true) with check (true);
create policy services on mind.guest_effect_deliveries to svc_input, svc_mind, svc_output using (true) with check (true);

-- input-svc 의 /health 와 격리함이 효과 쪽도 본다.
grant usage on schema mind to svc_input;
grant select on mind.effects, mind.guest_effects to svc_input;
grant select, update, delete on mind.effect_deliveries, mind.guest_effect_deliveries to svc_input;
grant select on mind.effect_consumers, mind.guest_effect_consumers to svc_input;

-- 소비자를 옮긴다. notify 가 못 받은 것은 새 받는 쪽으로, 에이전트가 못 받은 카드는 효과로.
insert into input.consumers (name, kinds) values
  ('mind-relay', array['*']),
  ('output-status', array['status.*', 'approval.*']);
insert into input.guest_consumers (name, kinds) values ('mind-relay-guest', array['*']);
insert into mind.effect_consumers (name, kinds) values
  ('output', array['*']),
  ('agent', array['mind.card', 'mind.card.closed', 'remind.*', 'checklist.shown']);
insert into mind.guest_effect_consumers (name, kinds) values
  ('output-guest', array['*']),
  ('agent-guest', array['remind.*']);

update input.deliveries d set consumer = 'output-status'
  from input.records r
 where d.consumer = 'notify' and r.id = d.id and r.received_at = d.at
   and (r.kind like 'status.%' or r.kind like 'approval.%');
update input.deliveries set consumer = 'mind-relay' where consumer = 'notify';
update input.guest_deliveries set consumer = 'mind-relay-guest' where consumer = 'notify-guest';

insert into mind.effects (kind, source, source_key, actor, channel, subject, occurred_at, received_at, schema_version, trace_id, causation_id, payload)
  select r.kind, r.source, r.source_key, r.actor, r.channel, r.subject, r.occurred_at, r.received_at, r.schema_version, r.trace_id, r.causation_id, r.payload
    from input.deliveries d join input.records r on r.id = d.id and r.received_at = d.at
   where d.consumer = 'agent';
insert into mind.effect_deliveries (consumer, id, at, subject)
  select 'agent', id, received_at, subject from mind.effects;
insert into mind.guest_effects (kind, source, source_key, actor, channel, subject, occurred_at, received_at, schema_version, trace_id, causation_id, payload)
  select r.kind, r.source, r.source_key, r.actor, r.channel, r.subject, r.occurred_at, r.received_at, r.schema_version, r.trace_id, r.causation_id, r.payload
    from input.guest_deliveries d join input.guest_records r on r.id = d.id and r.received_at = d.at
   where d.consumer = 'agent-guest';
insert into mind.guest_effect_deliveries (consumer, id, at, subject)
  select 'agent-guest', id, received_at, subject from mind.guest_effects;

delete from input.consumers where name in ('notify', 'agent');
delete from input.guest_consumers where name in ('notify-guest', 'agent-guest');

-- 에이전트는 이제 기록을 읽지 않는다(받은편지함에 쓰기만).
revoke select on input.records from svc_agent;
revoke all on input.deliveries, input.consumers from svc_agent;
