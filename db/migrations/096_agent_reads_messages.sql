-- 에이전트가 대화 메시지를 큐에서 집는다. 080 이 기록 쪽 권한을 거둔 뒤로 svc_agent 에는 이 권한이 없었다.
grant usage on schema input to svc_agent;
grant select on input.records to svc_agent;
grant select, update, delete on input.deliveries to svc_agent;
grant select, insert, update on input.consumers to svc_agent;
