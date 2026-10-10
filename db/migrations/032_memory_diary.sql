-- 기억 일지. 구조화 소스(캘린더·가계부·자산·운동·작업·사진)가 그래프에 사실을
-- 바로 넣을 때 한 줄씩 여기에도 적고, 6시간마다 그 구간의 줄을 묶어 하나의
-- 에피소드로 LLM 에 넣는다. 숫자는 직접 넣은 사실이 맡고, 일지는 결제한 곳·
-- 함께한 사람·일정을 대화에서 나온 같은 대상과 이어 주는 일을 맡는다.
create table if not exists memory_diary (
  id       bigserial primary key,
  grp      text not null,
  at       timestamptz not null default now(),
  line     text not null,
  used_at  timestamptz
);
create index if not exists memory_diary_pending on memory_diary (grp, at) where used_at is null;
