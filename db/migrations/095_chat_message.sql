-- 대화 메시지도 입구에 먼저 기록되고 에이전트가 큐에서 집는다(to-be 6). 에이전트가 재시작하는 동안 보낸 말이 사라지지 않는다.
-- 글은 봉인되어 있고 에이전트는 입구에 물어 읽는다(MESSAGE_KEY 를 갖지 않는다).
-- 손님 역할은 손님 기록 가운데 이것만 본다. 나머지(손님 폰의 위치 등)는 여전히 mind 를 거쳐 효과로만 받는다.
grant usage on schema input to svc_guest;
grant select on input.guest_records to svc_guest;
grant select, update, delete on input.guest_deliveries to svc_guest;
grant select, insert, update on input.guest_consumers to svc_guest;
create policy guest_own on input.guest_consumers to svc_guest using (name = 'agent-chat-guest') with check (name = 'agent-chat-guest');
create policy guest_own on input.guest_deliveries to svc_guest using (consumer = 'agent-chat-guest');
alter table input.guest_records enable row level security;
create policy guest_own on input.guest_records to svc_guest using (kind = 'chat.message');
create policy services on input.guest_records to svc_input, svc_mind using (true);
