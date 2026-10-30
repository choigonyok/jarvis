-- 작업이 mind 로 녹는다(to-be 4e). 작업은 mind 의 장부고, 읽기는 mind 의 창구, 쓰기는 명령(jobs.write)이다. 실행은 전과 같이
-- 에이전트의 스케줄러가 한다.
alter table public.jobs set schema mind;
alter table public.job_runs set schema mind;
alter table public.job_events set schema mind;
alter table public.job_memory set schema mind;
alter table public.job_records set schema mind;
alter table public.job_inbox set schema mind;
alter table public.job_sites set schema mind;

grant select, insert, update, delete on mind.jobs, mind.job_runs, mind.job_events, mind.job_memory, mind.job_records,
  mind.job_inbox, mind.job_sites to svc_mind;
do $$
declare s record;
begin
  for s in select c.oid::regclass as seq from pg_class c join pg_namespace n on n.oid = c.relnamespace
            where n.nspname = 'mind' and c.relkind = 'S' and c.relname like 'job%' loop
    execute format('grant usage on sequence %s to svc_mind', s.seq);
  end loop;
end $$;

-- 작업의 진행 기록은 그대로 트리거가 남긴다. 표가 옮겨 갔으니 찾는 곳만 고친다.
create or replace function input.job_event_record() returns trigger
language plpgsql security definer set search_path = pg_catalog, public, input as $$
declare
  owner_job boolean;
begin
  select who = 'owner' into owner_job from mind.jobs where id = new.job_id;
  if owner_job is not true then
    return null;
  end if;
  insert into input.outbox (payload) values (jsonb_build_object(
    'kind', 'job.' || new.kind, 'source', 'jobs', 'source_key', 'job_event:' || new.id,
    'actor', 'job:' || new.job_id, 'channel', 'job', 'subject', 'job:' || new.job_id,
    'occurred_at', new.at, 'tier', 'raw', 'schema_version', 1, 'trace_id', 'job:' || new.job_id,
    'payload', jsonb_build_object('job_id', new.job_id, 'run_id', new.run_id, 'kind', new.kind, 'text', new.text)));
  return null;
exception when others then
  raise warning '작업 기록을 남기지 못했습니다 (job_event %): %', new.id, sqlerrm;
  return null;
end $$;

drop owned by svc_jobs;
drop role svc_jobs;
