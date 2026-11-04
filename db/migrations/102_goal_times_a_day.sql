-- 하루에 여러 번 하는 목표(mind-svc internal/goals): "물 4번"처럼 그날 몇 번 했는지(count)를 적고,
-- 다 채우기 전의 날은 아직 했다·안 했다가 없다(mark ''). 하루 한 번인 목표는 count 를 쓰지 않는다(0).
alter table mind_goal_marks add column if not exists count integer not null default 0;
alter table mind_goal_marks drop constraint if exists mind_goal_marks_count_check;
alter table mind_goal_marks add constraint mind_goal_marks_count_check check (count >= 0);
alter table mind_goal_marks drop constraint if exists mind_goal_marks_mark_check;
alter table mind_goal_marks add constraint mind_goal_marks_mark_check check (mark in ('done', 'missed', ''));
