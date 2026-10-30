-- 결과 장부와 사실 표. to-be 구조의 mind 가 소유할 것들을 mind 스키마에 먼저 세운다.
-- 아직 아무도 이것을 읽고 판단하지 않는다(1단계, 동작 불변): 밤마다 mind_intents·mind_predictions·
-- mind_occurrences·mind_facts 에서 통째로 다시 만들고(POST /api/ledger/rebuild 도 같다), 상수는 이것으로 잰다.
create schema if not exists mind;
grant usage on schema mind to svc_mind;

-- 판단 하나에 줄 하나. 예측은 마감 때의 채점이 아니라 실제 발생으로 다시 채점한다 - 카드 알림이 늦게 들어와도
-- 그날의 결제는 있었던 것이다(10/28~30). 무시는 거절이 아니라 채점하지 않는다.
create table if not exists mind.outcomes (
  id          text primary key,            -- intent:<id> | prediction:<id>
  source      text not null,               -- card | told | quiet | notice | prediction
  skill       text not null default '',
  context     text not null default '',
  kind        text not null default '',
  rule        text not null default '',    -- 무엇이 올렸나: 그 자리의 이유, 예측의 키
  p           double precision not null,
  result      text not null,               -- model.Result*
  manual      boolean not null default false, -- 비서를 거치지 않고 사람이 직접 했다
  at          timestamptz not null,
  resolved_at timestamptz,
  body        jsonb not null               -- model.Outcome 전체
);
create index if not exists outcomes_group on mind.outcomes (source, skill, context);
grant select, insert, update, delete on mind.outcomes to svc_mind;

-- 사실과 그것이 맞는 기간. 하나만 맞는 사실(사는 곳, 직장)은 한 주어에 기간이 겹칠 수 없고, 겹치면 데이터베이스가
-- 거절한다. 이사가 옛 주소를 닫는 것은 코드의 약속이 아니라 이 제약이다.
create extension if not exists btree_gist;
create table if not exists mind.facts (
  id        text primary key,              -- mf_… 근거 장부(mind_facts)의 id
  subject   text not null,                 -- self | node:<기억의 노드> | 이름
  predicate text not null,
  object    text not null,
  text      text not null default '',
  valid     tstzrange not null,            -- [시작, 끝) 끝이 비었으면 아직 맞다
  p         double precision not null,
  single    boolean not null default false,
  exclude using gist (subject with =, predicate with =, valid with &&) where (single)
);
create index if not exists facts_subject on mind.facts (subject, predicate);
grant select, insert, update, delete on mind.facts to svc_mind;
