-- 약속. 지금의 calendar.json 이 옮겨올 자리다.
--
-- 날짜와 시각을 벽시계 문자열 그대로 두는 기존 결정은 유지한다: 한 시간대에서
-- 쓰는 개인 캘린더에서 "19:00"을 적고 "19:00"을 읽는 것이 옳고, 여름시간이
-- 없는 곳에서 timestamptz 로 왕복시키면 입력한 문자가 달라져 보일 여지만
-- 생긴다. 대신 충돌 검사는 구간 연산이라 정확해야 하므로, span 을 트리거가
-- 파생 컬럼으로 채운다. 사람이 쓰는 진실은 on_date/start_time 이고 span 은
-- 기계가 쓰는 사본이다.
create table if not exists commitments (
  -- 기존 파일의 'e-<epoch ms>-<seq>' 형식을 그대로 받는다. 마이그레이션이
  -- id 를 새로 발급하면 기존 SSE 프레임과 UI 키가 전부 어긋난다.
  id           text primary key,
  on_date      date not null,
  start_time   time,                       -- NULL = 종일 일정
  end_time     time,
  title        text not null,
  place        text,
  memo         text,
  participants bigint[] not null default '{}',
  status       text not null default 'confirmed'
                 check (status in ('confirmed', 'tentative', 'cancelled')),
  -- 누가 넣었는지. UI 가 읽고, 아무것도 분기하지 않는다 - 승인된 AI 변경과
  -- 손으로 적은 것은 똑같이 운영자의 결정이다.
  origin       text not null default 'me' check (origin in ('jarvis', 'me')),
  source_event_id bigint references raw_events(id),
  span         tstzrange,                  -- 트리거가 채운다. 직접 쓰지 말 것
  updated_at   timestamptz not null default now(),

  constraint commitments_end_needs_start
    check (end_time is null or start_time is not null),
  constraint commitments_end_after_start
    check (end_time is null or start_time is null or end_time >= start_time)
);

-- 벽시계를 구간으로 바꾼다.
--
-- 시간대는 세션 설정에서 읽고, 없으면 Asia/Seoul 이다. 컨테이너 기본값인 UTC
-- 로 계산하면 한국 시간 자정~09시의 일정이 하루 밀린 구간으로 저장된다.
--
-- 끝시각이 없는 일정에 1시간을 주는 것은 추측이다. 하지만 구간이 없으면 충돌
-- 검사에서 통째로 빠지고, "19시에 약속"이 아무것과도 겹치지 않는다고 답하는
-- 것이 1시간으로 가정하는 것보다 나쁘다.
create or replace function commitments_fill_span() returns trigger
language plpgsql as $$
declare
  tz text := coalesce(nullif(current_setting('jarvis.timezone', true), ''), 'Asia/Seoul');
begin
  if new.start_time is null then
    -- 종일 일정은 구간을 만들지 않는다. 하루를 다 막으면 그날의 모든 시간
    -- 약속이 충돌로 잡히고, 그건 "생일"이 "회의"와 겹친다는 뜻이 된다.
    new.span := null;
  else
    new.span := tstzrange(
      ((new.on_date + new.start_time) at time zone tz),
      ((new.on_date + coalesce(new.end_time, new.start_time + interval '1 hour'))
        at time zone tz),
      '[)'   -- 09:00 에 끝나는 일정과 09:00 에 시작하는 일정은 겹치지 않는다
    );
  end if;
  new.updated_at := now();
  return new;
end $$;

drop trigger if exists commitments_span on commitments;
create trigger commitments_span
  before insert or update on commitments
  for each row execute function commitments_fill_span();

-- 겹침을 배제 제약으로 막지 않는다. 일부러 겹쳐 잡는 일정이 있고(대기 중인
-- 두 후보, 옮길지 고민 중인 회의), 사람이 직접 쓰는 경로는 결재를 거치지
-- 않기로 한 제품이다. 여기서 INSERT 를 거부하면 캘린더 UI 가 고장난다.
-- 겹침은 막을 일이 아니라 알아차려서 제안할 일이므로, 인덱스만 둔다.
create index if not exists commitments_span_idx on commitments using gist (span);
create index if not exists commitments_on_date_idx on commitments (on_date);
create index if not exists commitments_participants_idx
  on commitments using gin (participants);
