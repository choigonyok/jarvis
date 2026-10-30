-- 대화와 운동이 입구의 투영이 된다(to-be 4e). 말한 일과 고친 일은 기록(chat.turn, workout.changes)이고, 이 표들은 그 기록을
-- 화면이 읽기 좋게 둔 것이다. input-svc 가 기록과 표를 한 트랜잭션에 쓴다. 옛 내역은 이 표가 원본이다.
alter table public.turns set schema input;
alter table public.workout_sets set schema input;
alter table public.workout_exercises set schema input;
alter table public.workout_sessions set schema input;
alter table public.routines set schema input;

grant select, insert, update, delete on input.turns, input.workout_sets, input.workout_exercises, input.workout_sessions,
  input.routines to svc_input;
grant usage on sequence input.workout_exercises_id_seq, input.workout_sets_id_seq to svc_input;

-- 운동의 리비전과 휴식 목표는 설정 표에 있다.
grant select, insert, update on public.settings to svc_input;
grant execute on function set_setting to svc_input;

drop owned by svc_chat;
drop role svc_chat;
drop owned by svc_workout;
drop role svc_workout;
