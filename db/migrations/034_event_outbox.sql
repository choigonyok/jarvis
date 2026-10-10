-- 이벤트 outbox. 서비스는 이벤트를 events-svc 에 바로 보내지 않고 여기에 먼저
-- 쓴다. events-svc 가 이 표를 비우면서 events 로 옮기므로, events-svc 가 꺼져
-- 있거나 재시작하는 동안 보낸 이벤트도 사라지지 않고 늦게 도착할 뿐이다.
-- payload 는 POST /events 본문과 같은 모양이다.
create table if not exists event_outbox (
  id         bigserial primary key,
  payload    jsonb not null,
  created_at timestamptz not null default now()
);
