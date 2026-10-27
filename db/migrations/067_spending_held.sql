-- 가계부 수집의 격리함(gocommon/health 의 규칙). 카드사 방의 메시지 하나를 기록하다 계속
-- 실패하면(데이터베이스는 멀쩡한데 그 메시지만) 그 메시지가 수집 전체를 막고 있었다 - 커서가
-- 그 앞에서 3초마다 같은 메시지를 영원히 다시 시도했다. 이제 다섯 번 실패하면 읽지 못한 알림
-- (spending_unparsed)으로 이유와 함께 빼고 커서를 넘긴다. 형식을 읽지 못한 알림과 같은
-- 자리라서 가계부 탭에서 똑같이 적거나 버린다.
--
-- reason 이 비어 있으면 형식을 읽지 못한 알림, 아니면 기록하지 못한 이유다.
alter table spending_unparsed add column if not exists reason text not null default '';
