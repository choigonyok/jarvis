-- 시간에 따라 바뀌는 사실. "운동 루틴이 언제 바뀌었고 그 전엔 어땠나"에
-- 답하는 표다.
--
-- 시간축이 둘인 것이 핵심이다:
--   valid_from / valid_to  세상에서 그 사실이 참이었던 구간
--   observed_at            우리가 그것을 알게 된 시각
-- 둘을 구분하지 않으면 뒤늦게 들어온 데이터를 처리할 수 없다. 3월에 루틴을
-- 바꿨는데 5월에야 알게 된 경우, 한 축만 있으면 "3월에 이미 알고 있었다"고
-- 거짓말을 하거나 "5월에 바꿨다"고 거짓말을 하게 된다.
--
-- valid_to 가 NULL 인 행이 현재 사실이다. 취소된 약속·끝난 루틴이 제안 근거로
-- 재활용되는 사고를 막는 것이 이 표의 존재 이유다.
create table if not exists routine_facts (
  id          bigserial primary key,
  subject     text not null,           -- 'workout.split' | 'sleep.bedtime'
  value       jsonb not null,
  valid_from  timestamptz not null,
  valid_to    timestamptz,             -- NULL = 지금도 참
  observed_at timestamptz not null default now(),
  source_event_id bigint references raw_events(id),

  constraint routine_facts_valid_order
    check (valid_to is null or valid_to > valid_from),

  -- 한 주제에 한 시점 하나의 사실. 겹치는 두 행이 들어오면 "지금 루틴이
  -- 무엇인가"에 답이 둘이 되고, 그건 답이 없는 것과 같다.
  -- 이것이 btree_gist 를 깐 이유다(subject with = 와 범위 &&를 섞는다).
  constraint routine_facts_no_overlap exclude using gist (
    subject with =,
    tstzrange(valid_from, valid_to) with &&
  )
);

create index if not exists routine_facts_current_idx
  on routine_facts (subject) where valid_to is null;

-- 특정 시점의 사실. 인자를 비우면 지금.
create or replace function fact_as_of(p_subject text, p_at timestamptz default now())
returns jsonb
language sql stable as $$
  select value from routine_facts
   where subject = p_subject
     and valid_from <= p_at
     and (valid_to is null or valid_to > p_at)
   limit 1
$$;

-- 새 사실을 주장한다. 열려 있던 이전 행을 닫고 새 행을 넣는다.
--
-- 이 함수가 있는 이유는 "이전 행 닫기"를 호출하는 쪽마다 손으로 쓰면 반드시
-- 한 곳에서 빼먹기 때문이다. 빼먹으면 배제 제약이 잡아주지만, 그때는 이미
-- INSERT 가 실패한 뒤다.
--
-- 같은 값을 다시 주장하면 아무것도 하지 않고 기존 행의 id 를 돌려준다.
-- 수집기가 같은 상태를 매일 보고하는 것은 정상이고, 그때마다 사실이 끊겼다
-- 이어지면 "언제 바뀌었나"의 답이 어제가 된다.
create or replace function assert_fact(
  p_subject  text,
  p_value    jsonb,
  p_from     timestamptz default now(),
  p_observed timestamptz default now(),
  p_event_id bigint default null
) returns bigint
language plpgsql as $$
declare
  current_row routine_facts;
  new_id bigint;
begin
  select * into current_row from routine_facts
   where subject = p_subject and valid_to is null
   order by valid_from desc limit 1;

  if found then
    if current_row.value = p_value then
      return current_row.id;                     -- 변한 것이 없다
    end if;
    -- 새 사실이 이전 사실보다 먼저 시작할 수는 없다. 순서가 뒤집힌 보고는
    -- 조용히 무시하는 대신 거부한다 - 조용히 버리면 왜 반영이 안 되는지
    -- 알아낼 방법이 없다.
    if p_from <= current_row.valid_from then
      raise exception '새 사실(%)이 현재 사실(%)보다 먼저 시작합니다: %',
        p_from, current_row.valid_from, p_subject;
    end if;
    update routine_facts set valid_to = p_from where id = current_row.id;
  end if;

  insert into routine_facts (subject, value, valid_from, observed_at, source_event_id)
  values (p_subject, p_value, p_from, p_observed, p_event_id)
  returning id into new_id;
  return new_id;
end $$;
