-- 어댑터가 읽어 둔 것의 가장 최근 값(to-be 4d). mind 는 이제 바깥을 부르지 않는다. 폰이 어디 있는지, 집이 어디인지는
-- 어댑터가 기록으로 알려 준 마지막 값을 읽는다. 기록에서 언제든 다시 만들 수 있는 투영이다.
create table mind.latest (
  kind    text primary key,
  payload jsonb not null,
  at      timestamptz not null
);
grant select, insert, update on mind.latest to svc_mind;

-- 구조화 레인. 어댑터가 먼저 뜨더라도 mind 가 뜨면 밀린 것부터 읽게 소비자를 미리 둔다.
insert into input.consumers (name, kinds) values
  ('mind-structured', array['calendar.near', 'calendar.year', 'context.visits', 'assets.holdings', 'context.now', 'context.now.guest', 'context.regions', 'context.travel']);
