-- 에이전트는 기록 가운데 대화 메시지만 본다. 나머지(위치·메시지·돈)는 효과로만 받던 대로다. 096 이 표 전체를 연 것을 좁힌다.
alter table input.records enable row level security;
create policy services on input.records to svc_input, svc_mind, svc_output using (true);
create policy agent_own on input.records to svc_agent using (kind = 'chat.message');

alter table input.deliveries enable row level security;
create policy services on input.deliveries to svc_input, svc_mind, svc_output using (true) with check (true);
create policy agent_own on input.deliveries to svc_agent using (consumer = 'agent-chat');

alter table input.consumers enable row level security;
create policy services on input.consumers to svc_input, svc_mind, svc_output using (true) with check (true);
create policy agent_own on input.consumers to svc_agent using (name = 'agent-chat') with check (name = 'agent-chat');
