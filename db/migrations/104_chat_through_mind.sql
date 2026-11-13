-- 대화에서 한 말과 카드의 답도 mind 를 거쳐 에이전트로 간다: 입구 → mind(mind-chat) → 효과 → 에이전트(agent-chat).
-- 에이전트가 받는 입력은 모두 mind 를 지난다. mind 는 기록의 키와 답의 카드만 넘기고, 글은 입구에 봉인된 채 남는다.
-- 에이전트와 손님 역할은 입구의 기록을 더 읽지 않는다(095~098 의 권한을 거둔다).
--
-- 돌고 있는 스택에 적용된다. 잠금을 5초 안에 얻지 못하면 아무것도 바꾸지 않고 실패하니 다시 돌리면 된다.
set local lock_timeout = '5s';

-- 손님 에이전트는 손님 효과에서 자기 몫 둘을 읽는다: mind 가 묻는 것(agent-guest)과 대화(agent-chat-guest).
-- 정책을 바꾸면 그 표가 잠기니 맨 먼저, 배달 표부터(배달을 처리하는 쪽과 같은 순서로) 한다.
drop policy if exists guest_own on mind.guest_effect_deliveries;
drop policy if exists guest_own on mind.guest_effect_consumers;
create policy guest_own on mind.guest_effect_deliveries to svc_guest
  using (consumer in ('agent-guest', 'agent-chat-guest'));
create policy guest_own on mind.guest_effect_consumers to svc_guest
  using (name in ('agent-guest', 'agent-chat-guest')) with check (name in ('agent-guest', 'agent-chat-guest'));

-- 네 소비자를 미리 등록해 둔다(086 과 같은 방식). 이 파일이 돈 뒤로 들어온 말과 답은 mind 와 에이전트가
-- 어느 순서로 다시 뜨든 사라지지 않고 차례를 기다린다.
insert into input.consumers (name, kinds) values ('mind-chat', array['chat.message', 'card.answer'])
  on conflict (name) do update set kinds = excluded.kinds;
insert into input.guest_consumers (name, kinds) values ('mind-chat-guest', array['chat.message', 'card.answer'])
  on conflict (name) do update set kinds = excluded.kinds;
insert into mind.effect_consumers (name, kinds) values ('agent-chat', array['chat.message', 'card.answer'])
  on conflict (name) do update set kinds = excluded.kinds;
insert into mind.guest_effect_consumers (name, kinds) values ('agent-chat-guest', array['chat.message', 'card.answer'])
  on conflict (name) do update set kinds = excluded.kinds;

-- 옛 에이전트가 아직 집지 않은 것(기다리는 것과 격리함의 것)은 mind 의 몫으로 옮긴다. 지금 쥐고 있는 것은
-- 그 에이전트가 이미 집었으니 옛 소비자와 함께 지운다.
update input.deliveries set consumer = 'mind-chat', lease_until = null
 where consumer = 'agent-chat' and (lease_until is null or lease_until <= now());
update input.guest_deliveries set consumer = 'mind-chat-guest', lease_until = null
 where consumer = 'agent-chat-guest' and (lease_until is null or lease_until <= now());
delete from input.deliveries where consumer = 'agent-chat';
delete from input.guest_deliveries where consumer = 'agent-chat-guest';
delete from input.consumers where name = 'agent-chat';
delete from input.guest_consumers where name = 'agent-chat-guest';

-- 에이전트는 입구의 기록·배달·소비자 표를, 손님 역할은 입구 자체를 읽지 않는다. outbox 에 넣는 일은 그대로라
-- svc_agent 의 스키마 사용 권한은 둔다. 095·097·098 의 행 보안 정책(agent_own, guest_own)은 남지만 권한이 없으면
-- 아무것도 읽지 못한다 - 정책을 지우려면 기록과 배달이 쉬지 않고 오가는 표를 잠가야 해서 두었다.
revoke all on input.records, input.deliveries, input.consumers from svc_agent;
revoke all on input.guest_records, input.guest_deliveries, input.guest_consumers from svc_guest;
revoke usage on schema input from svc_guest;
