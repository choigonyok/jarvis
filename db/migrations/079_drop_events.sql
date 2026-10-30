-- 4a 의 끝: 옛 길을 지운다. 이벤트는 078 에서 기록으로 옮겼고, 받은편지함·구독·격리함은 input 스키마에 있다.
drop view public.event_outbox;
drop table event_held;
drop table event_subscriptions;
drop table events;

drop owned by svc_events;
drop role svc_events;
