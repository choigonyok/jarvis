-- mind 가 memory-svc 로 보낸 구조화 기록(일정·지출·자산·운동·작업·사진). 서비스마다 읽는 곳을
-- mind 한 곳으로 합치면서, 같은 내용을 다시 보내지 않으려고 기록 키마다 마지막으로 보낸 내용의
-- 해시만 둔다. 원본은 각 서비스에, 그래프 사실은 memory-svc 에 있다.
create table if not exists mind_records (
  key     text primary key,                -- memory-svc 의 엣지 키(spending:812, calendar:…)
  hash    text not null,
  sent_at timestamptz not null
);

-- 관계 이름으로 사실을 찾는다(큰 결정 3개월 뒤 회고 등). 사실 본문은 jsonb 라 식 색인.
create index if not exists mind_facts_relation on mind_facts ((body->>'relation'), created_at);
