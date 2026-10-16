-- 로그인 정보 보관함(vault-svc). 운영자가 대화의 카드(비밀번호 칸)로 넣은 아이디·비밀번호를
-- 봉인해서(AES-256-GCM, 키는 맥미니 .env 의 JARVIS_VAULT_KEY - DB 와 백업에는 없다) 두고,
-- 브라우저가 그 사이트의 로그인 화면에 있을 때만 꺼내 준다. 모델은 값을 보지 못한다.
-- 역할은 050 처럼 비밀번호 없이 만든다. 비밀번호는 scripts/db-roles 가 맥미니 .env 의
-- JARVIS_DB_PASSWORD_VAULT 로 넣는다. 이 표들을 읽고 쓰는 역할은 svc_vault 하나뿐이다.
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'svc_vault') then
    create role svc_vault login;
  end if;
  execute format('grant connect on database %I to svc_vault', current_database());
end $$;
grant usage on schema public to svc_vault;

create table if not exists vault_logins (
  -- 로그인이 묶인 사이트(등록 도메인). login.coupang.com 이 아니라 coupang.com.
  domain       text primary key,
  name         text not null default '',
  -- 아이디를 알아볼 만큼만(ab***). 아이디·비밀번호 자체는 sealed 안에만 있다.
  hint         text not null default '',
  nonce        bytea not null,
  sealed       bytea not null,
  -- 어느 키로 봉인했는지(키의 해시 앞부분). 키가 바뀌면 다시 저장해야 한다.
  key_id       text not null,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  last_used_at timestamptz,
  uses         integer not null default 0
);

-- 꺼내 준 기록. 언제, 어느 페이지에, 무엇 때문에(대화·작업·재로그인), 그리고 못 준 것도.
create table if not exists vault_uses (
  id      bigserial primary key,
  at      timestamptz not null default now(),
  domain  text not null default '',
  host    text not null default '',
  purpose text not null default '',
  outcome text not null check (outcome in ('released', 'none', 'refused', 'failed')),
  note    text not null default ''
);
create index if not exists vault_uses_at on vault_uses (at desc);

grant select, insert, update, delete on vault_logins to svc_vault;
grant select, insert on vault_uses to svc_vault;
grant usage, select on sequence vault_uses_id_seq to svc_vault;
