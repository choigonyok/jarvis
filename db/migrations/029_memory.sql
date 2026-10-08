-- 기억(Graphiti) 수집 상태. 그래프 자체는 FalkorDB 에 있고, 여기에는 어디까지
-- 넣었는지와 무엇을 이미 넣었는지, 하루에 LLM 을 얼마나 썼는지만 둔다.
create table if not exists memory_state (
  source     text primary key,
  cursor     jsonb not null default '{}',
  last_run   timestamptz,
  last_error text not null default '',
  items      bigint not null default 0
);

-- 이미 넣은 것의 키(에피소드, 사실의 내용 해시). 같은 것을 두 번 넣지 않는다.
create table if not exists memory_done (
  key        text primary key,
  done_at    timestamptz not null default now()
);

-- 날짜별 추출 LLM 사용량. 하루 상한(MEMORY_DAILY_USD)을 넘으면 추출을 멈춘다.
create table if not exists memory_usage (
  day           date primary key,
  input_tokens  bigint not null default 0,
  output_tokens bigint not null default 0,
  calls         int not null default 0
);
