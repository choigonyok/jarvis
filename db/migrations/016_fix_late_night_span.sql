-- 004 의 트리거에 버그가 있었다: 23시 이후에 시작하고 끝시각이 없는 일정을
-- 저장할 수 없었다.
--
-- 끝시각이 없을 때 1시간을 더하는 계산을 `time` 공간에서 했는데, `time` 의
-- 덧셈은 자정에서 되돌아간다:
--
--   select time '23:00' + interval '1 hour';   -- 00:00:00
--
-- 그래서 구간이 [2026-09-25 23:00, 2026-09-25 00:00) 이 되고, 하한이 상한보다
-- 커서 tstzrange 생성 자체가 거부됐다 (SQLSTATE 22000). 시작 시각이 23:00~
-- 23:59 인 일정에서만 터지므로 낮 시간대만 써보는 동안은 드러나지 않았다.
--
-- 고침: 벽시계를 먼저 타임스탬프로 바꾸고, 더하기는 그쪽에서 한다. 타임스탬프
-- 덧셈은 날짜를 넘어간다.
--
-- 남는 제약: 23:00 에 시작해 01:00 에 끝나는 일정처럼 자정을 넘는 구간은 여전히
-- 넣을 수 없다. `end_time >= start_time` check 가 막고, 그건 파일 저장소 시절의
-- 규칙을 그대로 옮겨온 것이다. 하루 안에서 끝나지 않는 일정을 어떻게 적을지는
-- 저장 방식이 아니라 제품의 결정이므로 여기서 바꾸지 않는다.
create or replace function commitments_fill_span() returns trigger
language plpgsql as $$
declare
  tz    text := coalesce(nullif(current_setting('jarvis.timezone', true), ''), 'Asia/Seoul');
  start_at timestamptz;
begin
  if new.start_time is null then
    -- 종일 일정은 구간을 만들지 않는다. 하루를 다 막으면 그날의 모든 시간
    -- 약속이 충돌로 잡히고, 그건 "생일"이 "회의"와 겹친다는 뜻이 된다.
    new.span := null;
  else
    start_at := ((new.on_date + new.start_time) at time zone tz);
    new.span := tstzrange(
      start_at,
      case
        when new.end_time is null
          -- 추측이다. 하지만 구간이 없으면 충돌 검사에서 통째로 빠지고,
          -- "23시에 약속"이 아무것과도 겹치지 않는다고 답하는 것이 1시간으로
          -- 가정하는 것보다 나쁘다.
          then start_at + interval '1 hour'
        else ((new.on_date + new.end_time) at time zone tz)
      end,
      '[)'   -- 09:00 에 끝나는 일정과 09:00 에 시작하는 일정은 겹치지 않는다
    );
  end if;
  new.updated_at := now();
  return new;
end $$;

-- 이미 저장된 행의 span 을 다시 계산한다. 이 버그로 들어가지 못한 행은 없지만
-- (INSERT 가 거부됐으므로), 트리거를 고쳤으니 같은 함수로 만들어진 값이라는
-- 것을 보장해 두는 편이 낫다.
update commitments set updated_at = updated_at;
