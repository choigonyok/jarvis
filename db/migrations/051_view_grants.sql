-- 050 이 빠뜨린 뷰 권한. 050 의 목록은 표(pg_tables)에서만 뽑아서 뷰가 빠졌다 -
-- mind 가 선호를 계산하는 mind_attributes, workout 의 통계 함수와 루틴 상태가 읽는
-- workout_* 뷰. 뷰를 거쳐 읽는 함수(volume_by_group 등)는 호출한 역할의 권한으로 돈다.
-- (scripts/dbtest 가 svc_mind 로 돌린 store 테스트에서 드러났다.)

grant select on mind_attributes to svc_mind;
grant select on workout_session_volume, workout_group_volume, workout_routine_status to svc_workout;
