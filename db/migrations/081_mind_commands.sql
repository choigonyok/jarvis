-- mind 의 쓰기 API 를 지운다(to-be 4c). 바꾸는 일은 전부 기록으로 들어오고(reminder.set, intent.answer ...), mind 의
-- 결정적 레인이 처리한다. 부른 쪽은 답을 여기서 읽는다(GET /api/commands/{key}): 우편으로 보내고 창구에서 받는다.
-- 지금의 응답(저장된 때와 문구, 왜 안 되는지)은 그대로 돌려줘야 해서, 처리한 결과를 키로 남긴다. 같은 기록이 두 번
-- 와도 여기 있으면 다시 하지 않는다.
create table mind.commands (
  key    text primary key,          -- 기록의 source_key
  kind   text not null,
  who    text not null default 'owner',
  status integer not null,          -- 200, 400 ...
  body   jsonb not null,
  at     timestamptz not null default now()
);
create index commands_at on mind.commands (at);
grant select, insert, delete on mind.commands to svc_mind;

-- 기다리는 쪽이 있는 카드(결정 1). 대화의 권한 카드·결제 보류·작업의 답은 에이전트가 직접 이어 가고, mind 는 장부만
-- 닫는다. 관문의 예산·승인율·오늘의 카드에는 세지 않는다 - mind 가 판단해 올린 것이 아니다.
alter table mind_intents add column holder text;

-- 소비자. mind-commands 가 결정적 레인이다: 해석이 밀려도 답과 타이머는 밀리지 않는다.
insert into input.consumers (name, kinds) values
  ('mind-commands', array['intent.answer', 'payment.requested', 'reminder.*', 'checklist.*', 'note.*', 'link.*', 'timer.fired']);
insert into input.guest_consumers (name, kinds) values
  ('mind-commands-guest', array['reminder.*', 'checklist.*']);
