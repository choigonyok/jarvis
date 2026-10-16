-- 쓰이지 않는 테이블과 그 함수들을 지운다(U-1·U-2).
--
-- people·person_aliases·routine_facts·threads·memory_chunks·proposals·
-- proposal_feedback 은 초기 설계(사람·사실·회수 조각을 Postgres 에 둔다)의
-- 흔적이다. 사람과 사실은 memory-svc(그래프)와 mind 로, 제안은 agent 와
-- mind_intents 로 옮겨 갔고, 이 테이블들에는 행이 없고 읽는 코드도 없다.
-- market_* 셋은 026 에서 이미 지웠다.
--
-- vector 확장은 memory_chunks 와 recall() 만 썼다. 둘이 사라지면 쓰는 곳이
-- 없다. (이미지는 pgvector 그대로 둔다: 새로 설치할 때 001·009·013 이
-- 확장을 만들고 쓰기 때문이다. 적용된 마이그레이션은 고치지 않는다.)

drop function if exists person_by_alias(text, text);
drop function if exists fact_as_of(text, timestamptz);
drop function if exists assert_fact(text, jsonb, timestamptz, timestamptz, bigint);
drop function if exists suppressed(text, interval);
drop function if exists proposals_today(text);
drop function if exists recall(vector, bigint, integer, text[], integer);
drop function if exists open_threads(bigint, integer, integer);

drop table if exists proposal_feedback;
drop table if exists proposals;
drop table if exists person_aliases;
drop table if exists routine_facts;
drop table if exists threads;
drop table if exists memory_chunks;
drop table if exists people;

drop extension if exists vector;
