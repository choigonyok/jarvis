-- 종류는 데이터(to-be 5). 어떤 종류가 어느 레인으로 읽히는지, 행동이 어디까지 가는지, 슬롯, 한 답만 있는 관계, 관문의 숫자가
-- 코드의 목록이 아니라 표의 줄이다. 코드와 함께 오는 씨앗(by='seed') 줄은 코드를 따라가고, 사람이 고친 줄은 그대로 남는다.
create table mind.registry (
  section text not null,                 -- kind | action | slot | fact | policy
  key     text not null,
  body    jsonb not null,
  by_who  text not null default 'seed' check (by_who in ('seed', 'operator')),
  at      timestamptz not null default now(),
  primary key (section, key)
);
grant select, insert, update, delete on mind.registry to svc_mind;
