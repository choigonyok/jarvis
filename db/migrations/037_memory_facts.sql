-- 기억에 넣은 확인된 사실(mind 가 원문 인용으로 확인한 것)의 결과. Graphiti 의
-- add_triplet 이 사람·장소를 이미 아는 노드에 묶고 중복·모순을 정리한 결과를
-- ref 별로 남긴다. 같은 ref 가 다시 오면(outbox 재시도) Graphiti 를 다시 부르지
-- 않고 이 결과를 돌려준다.
create table if not exists memory_facts (
  ref     text primary key,
  grp     text not null,
  result  jsonb not null,
  at      timestamptz not null default now()
);
