-- 회수용 조각. 이 스키마에서 벡터가 사는 유일한 곳이다.
--
-- threads 에 embedding 컬럼을 두지 않고 여기로 모은 이유: 임베딩을 테이블마다
-- 하나씩 두면 HNSW 인덱스도 테이블마다 하나가 되고, "관련된 과거 맥락"을
-- 묻는 한 번의 회수가 여러 테이블에 흩어진 검색을 합치는 일이 된다. 조각의
-- 출처는 ref_table/ref_id 로 가리키면 되고, 그러면 운동 메모든 약속 맥락이든
-- 같은 인덱스 하나로 꺼낸다.
--
-- 정확한 사실(시각, 무게, 금액)은 여기에 묻지 않는다. 그건 각자의 표에
-- 있고, 여기 있는 것은 어디까지나 LLM 이 만든 서술이다.
create table if not exists memory_chunks (
  id          bigserial primary key,
  kind        text not null,            -- 'thread.summary' | 'workout.note' | 'proposal.rejected'
  ref_table   text,                     -- 'threads' | 'workout_sessions' | ...
  ref_id      text,                     -- 대상의 id. 테이블마다 타입이 달라 text
  person_ids  bigint[] not null default '{}',
  occurred_at timestamptz,              -- 회수 시 기간 필터의 기준
  body        text not null,
  -- 1536 = OpenAI text-embedding-3-small. 모델을 바꾸면 차원이 바뀌므로
  -- 새 컬럼이 아니라 새 마이그레이션으로 테이블을 바꿔 끼우고, 조각은
  -- raw_events 에서 다시 만든다. 한 테이블에 두 차원을 섞는 것보다 낫다.
  embedding   vector(1536),
  -- 어느 모델로 만든 벡터인지. 섞였을 때 알아차릴 수 있어야 한다.
  model       text,
  created_at  timestamptz not null default now(),

  unique (kind, ref_table, ref_id)
);

-- HNSW 는 IVFFlat 과 달리 미리 학습시킬 데이터가 필요 없다. 개인 규모에서
-- 조각이 수만 건이면 기본 파라미터로 충분하다.
create index if not exists memory_chunks_embedding_idx
  on memory_chunks using hnsw (embedding vector_cosine_ops);

create index if not exists memory_chunks_person_idx
  on memory_chunks using gin (person_ids);
create index if not exists memory_chunks_occurred_idx
  on memory_chunks (occurred_at desc);
create index if not exists memory_chunks_kind_idx on memory_chunks (kind, occurred_at desc);
