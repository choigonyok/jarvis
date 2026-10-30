-- mind 의 승인된 쓰기를 output 이 대신 한다(to-be 4d). 어댑터를 부르기 전에 줄을 남겨 같은 호출이 두 번 나가지 않게
-- 한다. 줄은 있는데 결과가 없으면 하던 중 끊긴 것이고, 주문은 다시 해 보고 알아낼 일이 아니라서 결과를 모른다고 답한다.
create table output.calls (
  key         text primary key,   -- 효과의 키
  service     text not null,
  method      text not null,
  path        text not null,
  status      integer,
  body        jsonb,
  started_at  timestamptz not null default now(),
  finished_at timestamptz
);
create index calls_started on output.calls (started_at);
grant select, insert, update, delete on output.calls to svc_output;

insert into input.consumers (name, kinds) values ('mind-results', array['execute.result']);
insert into mind.effect_consumers (name, kinds) values ('output-calls', array['adapter.call']);
