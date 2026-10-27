-- 운동의 룰 게이트용 질문(013)을 지운다. 그것을 읽던 workout-svc 의 GET /stats 는 부르는 곳이
-- 없었다 - 룰 게이트는 049 에서 사라졌고, 운동을 보고 먼저 말을 거는 것은 mind 가 운동 기록을 직접
-- 읽어 한다. 일정 충돌(conflicts_at·conflicts_for)은 calendar-svc 가 쓰므로 남긴다. 권한(050·051)은
-- 객체와 함께 사라진다.
drop function if exists volume_by_group(integer, text);
drop function if exists workout_gap_days(text);
drop view if exists workout_routine_status;
