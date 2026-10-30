-- 분류 캐시. 모델에게 한 번 물은 답을 적어 두고 다음부터 묻지 않는다. 모델의 답은 지워도 다시 물으면 되는 캐시이고,
-- 운영자가 고친 답은 다시 만들어지지 않는 값이라 모델의 답이 덮지 못한다(by_who). 지금은 가맹점 → 분류이고,
-- 상품 → 분류(mind_thing_classes)와 상식 판정(mind_link_cache)도 레지스트리 단계에서 여기로 모인다.
create table if not exists mind.classes (
  kind   text not null,                 -- merchant.category
  key    text not null,                 -- 가맹점 이름
  value  text not null,                 -- 분류
  p      double precision not null,
  by_who text not null check (by_who in ('llm', 'operator')),
  at     timestamptz not null default now(),
  primary key (kind, key)
);
grant select, insert, update, delete on mind.classes to svc_mind;
