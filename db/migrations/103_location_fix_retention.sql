-- 폰 좌표 기록(location.fix)은 입구에 이틀만 둔다.
--
-- context-svc 가 운영자 폰의 좌표를 1분에 한 번꼴로 기록으로 낸다. mind 가 그것으로 지도의 지역(수원,
-- 역 이름) 리마인드를 판단한다. 좌표 원본은 context-svc 가 CONTEXT_RAW_DAYS(기본 30일)만 두는데, 입구의
-- 원본층 보존기간(INPUT_RAW_RETAIN_DAYS, 기본 90일)을 따르면 좌표가 그보다 오래 남는다. 받는 쪽이 다
-- 가져간 좌표 기록은 이틀 뒤에 지운다. 나머지는 078 그대로다.
create or replace function input.prune_raw(p_before timestamptz) returns bigint
language plpgsql security definer set search_path = pg_catalog, input as $$
declare
  n bigint := 0;
  m bigint := 0;
begin
  with gone as (
    delete from input.records r
     where r.tier = 'raw'
       and (r.received_at < p_before or r.kind like 'location.%fix' and r.received_at < now() - interval '2 days')
       and not exists (select 1 from input.deliveries d where d.id = r.id)
    returning r.source, r.source_key
  )
  delete from input.record_keys k using gone g where k.source = g.source and k.source_key = g.source_key;
  get diagnostics n = row_count;
  with gone as (
    delete from input.guest_records r
     where r.tier = 'raw'
       and (r.received_at < p_before or r.kind like 'location.%fix' and r.received_at < now() - interval '2 days')
       and not exists (select 1 from input.guest_deliveries d where d.id = r.id)
    returning r.source, r.source_key
  )
  delete from input.record_keys k using gone g where k.source = g.source and k.source_key = g.source_key;
  get diagnostics m = row_count;
  return n + m;
end $$;
revoke all on function input.prune_raw(timestamptz) from public;
grant execute on function input.prune_raw(timestamptz) to svc_input;
