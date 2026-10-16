-- 폰 알림(phonenote-svc). 맥에 아이폰 미러링으로 들어온 알림을 맥의 수집기가 읽어 보내고,
-- 이 서비스가 해석해서 의미 있는 것은 다른 데이터에 반영한다(배민 결제에 주문한 메뉴를
-- 붙이는 식). 해석하지 않을 앱(회사 업무 앱, 카톡·문자처럼 따로 읽는 앱, 메일)은 수집기가
-- 맥에서부터 보내지 않는다.
-- 역할은 050 처럼 비밀번호 없이 만든다. 비밀번호는 scripts/db-roles 가 맥미니 .env 의
-- JARVIS_DB_PASSWORD_PHONENOTE 로 넣는다.
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'svc_phonenote') then
    create role svc_phonenote login;
  end if;
  execute format('grant connect on database %I to svc_phonenote', current_database());
end $$;
grant usage on schema public to svc_phonenote;

create table if not exists phone_notes (
  id          bigserial primary key,
  device      text not null default '',
  -- 수집기가 붙인 그 기기 안의 id. 같은 알림을 두 번 받으면 한 번만 남는다.
  source_key  text not null,
  app         text not null,
  title       text not null default '',
  subtitle    text not null default '',
  body        text not null default '',
  posted_at   timestamptz not null,
  received_at timestamptz not null default now(),
  status      text not null default 'new' check (status in ('new', 'read', 'done', 'skipped', 'failed')),
  -- 해석: 무슨 알림인지(kind), 의미가 있는지, 읽어 낸 값들(가게·메뉴·금액…).
  reading     jsonb,
  -- 반영한 것: "가계부 #812 에 상품 2개".
  applied     text not null default '',
  error       text not null default '',
  tries       integer not null default 0,
  unique (device, source_key)
);
create index if not exists phone_notes_posted on phone_notes (posted_at desc);
create index if not exists phone_notes_open on phone_notes (status) where status in ('new', 'read');

-- 수집기가 마지막으로 연락한 때와 상태(권한이 없어 못 읽는다 같은) - 상태 탭이 본다.
create table if not exists phone_collectors (
  device    text primary key,
  seen_at   timestamptz not null default now(),
  problem   text not null default '',
  last_key  text not null default ''
);

grant select, insert, update, delete on phone_notes, phone_collectors to svc_phonenote;
grant usage, select on sequence phone_notes_id_seq to svc_phonenote;
-- status-svc 는 상태 탭의 "폰 알림" 줄을 위해 읽기만.
grant select on phone_notes, phone_collectors to svc_status;
