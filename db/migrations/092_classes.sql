-- 분류 캐시를 표 하나로(to-be 5). 가맹점 규칙(운영자가 가르친 값), 상품 분류, 상식 판정이 mind.classes 의 kind 로 들어간다.
-- 모델의 답은 지워도 다시 물으면 되는 캐시이고, 운영자의 답은 값이라 모델이 덮지 못한다(by_who).
alter table mind.classes add column detail jsonb not null default '{}';

insert into mind.classes (kind, key, value, p, by_who, at)
select 'merchant.rule', merchant_key, category, 1, 'operator', updated_at from mind.merchant_rules
on conflict do nothing;

insert into mind.classes (kind, key, value, p, by_who, at)
select 'thing.class', name, class, 0.8, 'llm', at from public.mind_thing_classes
on conflict do nothing;

insert into mind.classes (kind, key, value, p, by_who, at, detail)
select 'link.judge', thing_class || E'\x1f' || plan_kind, rel, plausibility, 'llm', at, jsonb_build_object('why', why)
  from public.mind_link_cache
on conflict do nothing;

drop table mind.merchant_rules;
drop table public.mind_thing_classes;
drop table public.mind_link_cache;
