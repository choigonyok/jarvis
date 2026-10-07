-- 대화 스레드의 요약. 카톡이 첫 번째 생산자다.
--
-- 카톡 원문 메시지는 raw_events 에 있고 여기 오지 않는다. 메시지 한 줄씩을
-- 회수 단위로 삼으면 "윤석이랑 얘기하던 그 건"이 200개의 조각으로 흩어져서,
-- 무엇을 꺼내도 맥락이 빠진 한 줄이 나온다. 대화가 끊긴 뒤 한 덩어리로
-- 요약하고, 그 요약에 구조화된 필드를 붙이는 것이 회수의 단위다.
--
-- resolution 이 별도 컬럼인 이유: "결론이 뭐였지"가 실제로 가장 많이 묻는
-- 질문이고, 요약 본문에 섞여 있으면 벡터 검색으로는 꺼낼 수 있어도 "아직
-- 결론이 없는 스레드만" 같은 조건으로는 못 꺼낸다.
create table if not exists threads (
  id           bigserial primary key,
  source       text not null default 'kakaotalk',
  chat_key     text,                        -- 소스 안에서의 방 식별자
  participants bigint[] not null default '{}',
  started_at   timestamptz,
  ended_at     timestamptz,
  topic        text,
  status       text not null default 'open'
                 check (status in ('open', 'resolved', 'dropped')),
  resolution   text,
  summary      text,
  -- 이 요약이 어느 원본 구간에서 나왔는지. 재생할 때 다시 요약할 범위다.
  first_event_id bigint references raw_events(id),
  last_event_id  bigint references raw_events(id),
  message_count  int not null default 0,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create index if not exists threads_participants_idx
  on threads using gin (participants);
create index if not exists threads_ended_idx on threads (ended_at desc);
create index if not exists threads_chat_idx on threads (source, chat_key, ended_at desc);
-- 아직 결론이 없는 것들. 선제 제안이 가장 자주 쓰는 목록이다.
create index if not exists threads_open_idx
  on threads (updated_at desc) where status = 'open';
