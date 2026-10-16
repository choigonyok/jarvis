-- memory-svc 의 6시간 일지(diary)를 지운다(U-6). 구조화 기록은 mind 가 /records 로 넣고,
-- 일지는 그것을 LLM 에 한 번 더 넣어 같은 사실을 두 번 뽑았다. 기본 수집 목록에서 뺀 뒤에도
-- 사실마다 일지 줄이 쌓이고 있었다 - 읽는 곳 없이.
--
-- memory-svc 는 이제 이벤트를 내지 않는다(U-8: memory.plan 을 받는 곳이 없었다).

drop table if exists memory_diary;
revoke insert on event_outbox from svc_memory;
