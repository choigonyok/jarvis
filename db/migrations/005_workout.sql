-- 운동. 지금의 workout.json(Book) 이 옮겨올 자리다.
--
-- 중첩 JSON 을 그대로 jsonb 한 칸에 넣지 않고 세트까지 펼치는 이유는 룰
-- 게이트 때문이다. "3주간 하체 볼륨이 얼마였나", "이 종목 최고 세트가
-- 언제였나"가 SQL 한 줄이어야 제안이 값싸게 나온다. jsonb 로 두면 그 질문이
-- 전부 애플리케이션 코드로 올라온다.
--
-- 파생값(볼륨, 1RM 추정, 연속일)은 저장하지 않는다. 실제로 기록한 세트와
-- 어긋날 수 있는 두 번째 숫자를 만들지 않는다는 기존 결정을 그대로 따른다.

create table if not exists routines (
  id         text primary key,
  name       text not null,
  exercises  text[] not null default '{}',   -- 훈련하는 순서대로의 종목 이름
  sort_order int not null default 0,
  updated_at timestamptz not null default now()
);

create table if not exists workout_sessions (
  id           text primary key,            -- 기존 's-<...>' 형식 유지
  on_date      date not null,
  -- 루틴은 이름으로도 남긴다. 루틴을 지우거나 이름을 바꿨다고 해서 실제로
  -- 한 운동의 기록이 지워지면 안 된다.
  routine_id   text references routines(id) on delete set null,
  routine_name text,
  started_at   timestamptz not null,
  ended_at     timestamptz,                 -- NULL = 진행 중
  note         text,
  body_weight  numeric(5,2),                -- kg, 하루 한 번
  source_event_id bigint references raw_events(id),
  updated_at   timestamptz not null default now()
);

create index if not exists workout_sessions_date_idx on workout_sessions (on_date desc);
create index if not exists workout_sessions_started_idx on workout_sessions (started_at desc);
-- "지금 진행 중인 세션이 있나" 는 화면이 열릴 때마다 묻는 질문이다.
create index if not exists workout_sessions_running_idx
  on workout_sessions (started_at desc) where ended_at is null;

create table if not exists workout_exercises (
  id           bigserial primary key,
  session_id   text not null references workout_sessions(id) on delete cascade,
  ord          int  not null,               -- 화면에 보이는 순서
  name         text not null,
  -- 부위는 이름에서 추론하지 않고 기록 시점에 박아둔다. 추론 표가 나중에
  -- 바뀌어도 과거 주간 볼륨이 흔들리지 않아야 한다.
  muscle_group text,
  note         text,
  unique (session_id, ord)
);

create index if not exists workout_exercises_name_idx on workout_exercises (lower(name));

create table if not exists workout_sets (
  id          bigserial primary key,
  exercise_id bigint not null references workout_exercises(id) on delete cascade,
  ord         int not null,
  weight      numeric(6,2) not null default 0,  -- 맨몸은 0, reps 는 그대로 센다
  reps        int not null default 0,
  done        boolean not null default false,
  -- 워밍업은 한 일이지만 치는 일은 아니다. 빈 봉 세트를 주간 볼륨에 넣으면
  -- 무게를 올릴지 판단하는 데 쓸 수 없는 숫자가 된다.
  warmup      boolean not null default false,
  unique (exercise_id, ord)
);

-- 세션당 볼륨. 완료한 본세트만, 워밍업 제외 - 애플리케이션의 volumeOf() 와
-- 같은 정의여야 하므로 여기 한 곳에만 둔다.
create or replace view workout_session_volume as
select s.id,
       s.on_date,
       s.routine_name,
       s.started_at,
       s.ended_at,
       coalesce(sum(w.weight * w.reps), 0)::numeric as volume_kg,
       count(w.id)                                  as done_sets
  from workout_sessions s
  left join workout_exercises e on e.session_id = s.id
  left join workout_sets w
         on w.exercise_id = e.id and w.done and not w.warmup
 group by s.id;

-- 부위별 볼륨. "이번 주에 하체 했나"에 답하는 것이 이 표의 용도다.
create or replace view workout_group_volume as
select s.on_date,
       coalesce(e.muscle_group, '기타') as muscle_group,
       coalesce(sum(w.weight * w.reps), 0)::numeric as volume_kg
  from workout_sessions s
  join workout_exercises e on e.session_id = s.id
  left join workout_sets w
         on w.exercise_id = e.id and w.done and not w.warmup
 group by s.on_date, coalesce(e.muscle_group, '기타');
