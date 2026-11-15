-- 운동 세션을 누가 했는지(made_by): 운영자 'owner' 또는 손님 'guest'. 두 사람이 한 권을 쓰는데 세션에 주인이 없어서,
-- 한 사람의 진행 중 세션이 다른 사람의 운동 화면을 차지했고 지난번·최고 기록·주간 목표가 두 사람의 세션을 섞었다.
-- input-svc 가 세션을 처음 쓰는 저장의 토큰으로 박고, 뒤의 저장은 누가 하든 바꾸지 않는다. 화면이 보낸 값은 읽지 않는다.
-- 이 칸이 생기기 전의 세션은 비어 있다 - 누가 했는지 모르는 것을 짐작해 채우지 않는다(화면은 두 사람 모두의 것으로 둔다).
-- svc_input 은 이 표에 이미 쓰기 권한이 있다(088).
--
-- 돌고 있는 스택에 적용된다. 잠금을 5초 안에 얻지 못하면 아무것도 바꾸지 않고 실패하니 다시 돌리면 된다.
set local lock_timeout = '5s';

alter table input.workout_sessions add column if not exists made_by text;
alter table input.workout_sessions drop constraint if exists workout_sessions_made_by_check;
alter table input.workout_sessions add constraint workout_sessions_made_by_check check (made_by in ('owner', 'guest'));
