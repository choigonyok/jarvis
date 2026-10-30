-- 카드의 답도 입구로 들어오고 그 답을 기다리는 쪽(에이전트)이 기록에서 바로 읽는다(to-be 7). mind 가 꺼져 있어도 결제가 이어진다.
drop policy agent_own on input.records;
create policy agent_own on input.records to svc_agent using (kind in ('chat.message', 'card.answer'));
drop policy guest_own on input.guest_records;
create policy guest_own on input.guest_records to svc_guest using (kind in ('chat.message', 'card.answer'));
