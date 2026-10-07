-- 진실의 원천. 수집기가 본 것을 해석하기 전에 그대로 적는다.
--
-- 이 테이블이 추가 전용인 이유는 감사 때문이 아니라 재생(replay) 때문이다.
-- 파생 테이블·요약·임베딩은 전부 여기서 다시 만들 수 있어야 하고, 그래야
-- 나중에 추출 방식을 바꾸거나 메모리 레이어를 바꿔 끼우는 결정을 미룰 수
-- 있다. 여기에 UPDATE 를 한 번 허용하면 그 선택지가 사라진다.
--
-- 봉투는 CloudEvents 를 따른다. 소스가 5개를 넘어가면 판단 쪽이 소스별
-- if 문 더미가 되는데, 규격을 처음부터 고정하는 것이 그것보다 싸다.
create table if not exists raw_events (
  id          bigserial primary key,
  source      text        not null,           -- 'kakaotalk' | 'calendar' | 'workout'
  type        text        not null,           -- 'message.created' | 'session.ended'
  subject     text,                           -- 소스 안에서의 대상 (방 id, 종목 코드)
  ext_id      text,                           -- 소스가 부여한 id. 멱등성의 근거
  occurred_at timestamptz not null,           -- 세상에서 일어난 시각
  ingested_at timestamptz not null default now(),  -- 우리가 알게 된 시각
  data        jsonb       not null default '{}'::jsonb,
  trace_id    text                            -- 이 이벤트에서 비롯된 제안까지 잇는 끈
);

-- (source, ext_id) 가 수집기의 멱등 키다.
--
-- ext_id 가 NULL 인 행끼리는 충돌하지 않는다(NULL 은 서로 다르게 취급된다).
-- 이건 의도된 것이다: 커서를 못 만드는 수집기는 중복을 감수하고, 커서가 있는
-- 수집기는 ext_id 를 채워서 재시작 후 재생이 두 번째 행을 만들지 않게 한다.
create unique index if not exists raw_events_source_ext_key
  on raw_events (source, ext_id)
  where ext_id is not null;

create index if not exists raw_events_occurred_idx
  on raw_events (occurred_at desc);

-- 룰 게이트가 가장 자주 하는 질문: "이 소스의 이 종류가 최근에 뭐가 있었나".
create index if not exists raw_events_source_type_idx
  on raw_events (source, type, occurred_at desc);

-- jsonb_path_ops 는 기본 연산자 클래스보다 작고, 우리가 쓰는 것은 @> 뿐이다.
create index if not exists raw_events_data_idx
  on raw_events using gin (data jsonb_path_ops);

-- 추가 전용을 문서가 아니라 규칙으로 만든다. 행 단위가 아니라 문장 단위인
-- 이유는 막는 것이 목적이고, 어느 행이 걸렸는지 알 필요는 없기 때문이다.
--
-- 정말 지워야 할 때(잘못 수집한 소스를 통째로 버릴 때)는:
--   alter table raw_events disable trigger raw_events_append_only;
--   ... 정리 ...
--   alter table raw_events enable trigger raw_events_append_only;
-- 한 줄로 끄고 켤 수 있게 둔 것도 의도다. 끄는 것을 어렵게 만들면 사람들은
-- 트리거를 아예 안 만든다.
create or replace function raw_events_reject_mutation() returns trigger
language plpgsql as $$
begin
  raise exception 'raw_events 는 추가 전용입니다 (% 시도). 파생 테이블을 고치세요.', tg_op;
end $$;

drop trigger if exists raw_events_append_only on raw_events;
create trigger raw_events_append_only
  before update or delete on raw_events
  for each statement execute function raw_events_reject_mutation();
