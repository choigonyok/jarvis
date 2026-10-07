-- 대화를 사람별로 나눈다. '' 는 운영자의 대화(지금까지의 모든 턴), 'guest' 는
-- 손님 계정의 대화다. 손님은 별도 에이전트(agent-guest)가 받고, 그 에이전트는
-- 자기 thread 만 읽고 쓴다 - 운영자의 대화가 손님 화면에도, 손님 쪽 모델의
-- 맥락에도 들어가지 않는 것이 목적이다.
alter table turns add column if not exists thread text not null default '';
create index if not exists turns_thread_at_idx on turns (thread, at);
