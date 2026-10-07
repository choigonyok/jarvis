-- uniple(커플 앱) 캘린더의 로그인 세션. calendar-svc 가 일정을 uniple 에서
-- 읽고 쓸 때 쓴다.
--
-- 리프레시 토큰은 쓸 때마다 새것으로 바뀐다. 환경변수에 둔 값은 첫 한 번만
-- 유효하므로, 바뀐 토큰을 여기 적어 두지 않으면 재시작할 때마다 로그인이
-- 풀린다. seed 는 이 토큰이 어떤 환경변수 값에서 시작됐는지다 - 환경변수가
-- 그것과 다르면 사람이 새로 로그인해 넣은 것이므로 그쪽을 따른다.
create table if not exists uniple_session (
  id            int primary key default 1 check (id = 1),
  refresh_token text not null,
  seed          text not null,
  updated_at    timestamptz not null default now()
);
