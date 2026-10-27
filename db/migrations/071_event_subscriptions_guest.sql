-- 손님 쪽 구독. 손님 agent 가 손님 토큰으로 등록한 구독(이름은 -guest 로 끝난다)이다.
-- 여기에는 손님에게 가는 이벤트(notify.audience = 'guest': 손님 리마인드 같은 것)만,
-- 손님 토큰을 달아 보낸다. 운영자 쪽 이벤트는 아무것도 닿지 않고, 운영자 토큰도 가지 않는다.
--
-- 한쪽의 구독을 다른 쪽 토큰으로 덮어쓰지 못한다: 손님 토큰은 손님 쪽 이름만 등록하고,
-- 이미 있는 구독의 쪽은 다시 등록해도 바뀌지 않는다(409).
alter table event_subscriptions add column if not exists guest boolean not null default false;
