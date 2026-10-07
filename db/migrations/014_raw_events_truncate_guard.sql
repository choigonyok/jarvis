-- 002 의 추가 전용 트리거에는 구멍이 있었다: before update or delete 는
-- TRUNCATE 를 막지 않는다. TRUNCATE 는 별개의 트리거 이벤트이고, 하필 로그
-- 전체를 한 번에 날리는 쪽이다.
--
-- 같은 함수를 재사용한다. 메시지의 tg_op 가 'TRUNCATE' 로 찍히므로 무엇이
-- 막혔는지도 그대로 읽힌다.
drop trigger if exists raw_events_no_truncate on raw_events;
create trigger raw_events_no_truncate
  before truncate on raw_events
  for each statement execute function raw_events_reject_mutation();
