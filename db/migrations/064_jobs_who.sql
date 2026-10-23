-- 작업의 주인. 운영자(owner)와 손님(guest)은 각자 자기 작업만 보고 만들고 고치며,
-- 각자의 에이전트(agent, agent-guest)가 자기 작업만 실행한다. jobs-svc 가 토큰으로
-- 누구인지 정하고 모든 조회·변경을 그 사람의 작업으로 묶는다.
alter table jobs add column if not exists who text not null default 'owner';
do $$ begin
  alter table jobs add constraint jobs_who_check check (who in ('owner', 'guest'));
exception when duplicate_object then null; end $$;
create index if not exists jobs_who_idx on jobs (who, state);
