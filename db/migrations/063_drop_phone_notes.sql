-- 폰 알림(phonenote-svc)을 지운다(2026-10-22). 아이폰 미러링으로 맥에 들어온 알림을 수집기가 읽어
-- 해석하던 기능을 운영자가 통째로 걷어 냈다: 미러링 알림은 폰이 그 맥 가까이 있어야 오고, 수집기는
-- 알림 저장소를 읽을 권한을 받은 적이 없어 표에는 알림이 한 건도 없었다.
--
-- 056 은 적용된 마이그레이션이라 고치지 않는다 - 새로 설치하면 만들었다가 여기서 지운다.
-- 역할 svc_phonenote 도 지운다: 남은 권한(데이터베이스 connect, 스키마 usage)을 걷고 나서.

drop table if exists phone_notes;
drop table if exists phone_collectors;

do $$
begin
  if exists (select 1 from pg_roles where rolname = 'svc_phonenote') then
    drop owned by svc_phonenote;
    drop role svc_phonenote;
  end if;
end $$;
