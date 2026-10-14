-- 링크의 두 단계 판정 중 "상식" 쪽. 특정 사건이 아니라 (상품 분류, 일정 종류) 쌍에 대해
-- 한 번 묻고 다시 쓴다: 장작은 캠핑을 위해 사는 물건인가(0.9), 프로틴은(0.05).
create table if not exists mind_thing_classes (
  name  text primary key,                -- 상품 이름 그대로 ("참나무 장작 10kg")
  class text not null,                   -- 짧은 분류 ("장작")
  at    timestamptz not null default now()
);

create table if not exists mind_link_cache (
  thing_class  text not null,
  plan_kind    text not null,            -- 일정 종류(캠핑, 데이트 …)
  rel          text not null,            -- prep_for | used_during | unrelated
  plausibility real not null,
  why          text not null default '',
  at           timestamptz not null default now(),
  primary key (thing_class, plan_kind)
);

-- 반복: 그 종류의 일정 total 번 가운데 support 번, 시작 gap_bucket 전에 그 분류의 물건을
-- 샀다. 원본(발생·일정)에서 다시 셀 수 있는 집계다 - 밤마다 그날 끝난 일정만 더하고,
-- POST /api/patterns/rebuild 로 처음부터 다시 만든다.
create table if not exists mind_pattern_counts (
  kind_a     text not null,              -- 일정 종류
  rel        text not null,              -- prep_for
  kind_b     text not null,              -- 물건 분류
  gap_bucket text not null,              -- d1 | d3 | d7 | d14 (일정 시작 며칠 전 안)
  support    integer not null default 0,
  total      integer not null default 0,
  last_seen  timestamptz,
  primary key (kind_a, rel, kind_b, gap_bucket)
);
-- 그 종류의 일정이 몇 번 있었는지(분모). kind_b = '' 인 행.
