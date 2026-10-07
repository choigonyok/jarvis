-- workout-svc 를 붙이면서 드러난 두 가지.
--
-- 1. client_id
-- 화면은 종목마다 'e-<ts>-<i>' 형식의 id 를 만들어 쓰고, 세트를 고칠 때 그
-- id 로 찾는다. 서버가 저장하면서 그 id 를 버리고 (session_id, ord) 만 남기면,
-- 저장 후 다시 불러올 때마다 id 가 새로 발급되어 화면의 React 키가 전부
-- 갈린다. 운동 중에 저장이 600ms 마다 일어나므로 이건 실제로 눈에 보인다.
--
-- 2. content_hash
-- 화면은 책 전체를 PUT 한다(기록 하나를 고쳐도 전체). 세션마다 종목·세트를
-- 지우고 다시 넣으면 세트를 한 번 체크할 때마다 수천 행이 오간다. 세션 내용의
-- 해시를 들고 있으면 바뀐 세션만 다시 쓰면 되고, 운동 중에 바뀌는 것은 대개
-- 진행 중인 세션 하나뿐이다.
alter table workout_exercises add column if not exists client_id text;
alter table workout_sessions  add column if not exists content_hash text;

-- 한 세션 안에서 화면이 준 id 는 유일하다.
create unique index if not exists workout_exercises_client_idx
  on workout_exercises (session_id, client_id) where client_id is not null;
