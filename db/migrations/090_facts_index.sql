-- 사실의 원천을 하나로(to-be 4f). 기억 그래프(Graphiti)는 이 표에서 다시 만들 수 있는 색인이다. 표에는 세 가지가 있다.
--   checked  mind 가 근거와 대조한 사실(mind_facts 에서 밤마다 다시 만든다)
--   record   구조화 기록(결제·일정·운동·작업·사진), 보낼 때마다 한 줄
--   legacy   이 표가 생기기 전에 그래프에 들어간 줄. 한 번 옮겨 두고 고치지 않는다
-- body 는 그 줄을 그래프에 어떻게 넣는지다(memory-svc 가 다시 만들 때 읽는다). 비어 있으면 그래프에 넣지 않는다.
alter table mind.facts
  add column origin text not null default 'checked' check (origin in ('checked', 'record', 'legacy')),
  add column body jsonb not null default '{}';
create index facts_origin on mind.facts (origin, id);
