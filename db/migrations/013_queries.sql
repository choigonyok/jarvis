-- 룰 게이트가 실제로 부르는 질문들.
--
-- 함수로 만들어 둔 이유는 재사용이 아니라 정의를 한 곳에 묶기 위해서다.
-- "충돌"이 무엇인지, "공백"이 며칠부터인지를 서비스마다 각자 정하면, 제안을
-- 만드는 쪽과 화면에 보여주는 쪽이 다른 답을 내놓는다.

-- ── 1. 일정 충돌 ────────────────────────────────────────────────────────
-- 이 구간에 겹치는 확정 약속. 종일 일정은 span 이 없어서 잡히지 않는다.
--
-- p_buffer 는 이동시간이다. 19시 강남 약속과 20시 판교 약속은 겹치지 않지만
-- 둘 다 갈 수는 없고, 그 사실을 알아차리는 유일한 방법이 여유를 넣고 보는
-- 것이다. 기본값을 0 으로 둔 것은 이동시간을 모르는 채 30분을 가정하면
-- 붙여 잡은 일정마다 충돌이라고 말하게 되기 때문이다.
create or replace function conflicts_at(
  p_span    tstzrange,
  p_exclude text     default null,
  p_buffer  interval default interval '0'
) returns setof commitments
language sql stable as $$
  select * from commitments
   where status = 'confirmed'
     and span is not null
     and (id is distinct from p_exclude)
     and span && tstzrange(lower(p_span) - p_buffer, upper(p_span) + p_buffer)
   order by lower(span)
$$;

-- 이미 저장된 약속이 다른 무엇과 겹치는지. 새 약속이 들어온 직후 트리거가
-- 묻는 형태다.
create or replace function conflicts_for(
  p_id     text,
  p_buffer interval default interval '0'
) returns setof commitments
language sql stable as $$
  select c.* from commitments self
  cross join lateral conflicts_at(self.span, self.id, p_buffer) c
   where self.id = p_id and self.span is not null
$$;

-- ── 2. 운동 공백 ────────────────────────────────────────────────────────
-- 마지막으로 끝낸 세션에서 며칠 지났나. 기록이 없으면 NULL.
create or replace function workout_gap_days(p_tz text default 'Asia/Seoul')
returns int
language sql stable as $$
  select ((now() at time zone p_tz)::date - max(on_date))::int
    from workout_sessions where ended_at is not null
$$;

-- 루틴별로 마지막에 한 날. 가장 오래 안 한 것이 위로 온다.
--
-- 한 번도 안 한 루틴이 맨 위인 이유: 손대지 않은 날이 2주 전에 한 날보다 더
-- 밀린 것이다. NULL 을 먼저 놓는 것으로 그걸 표현한다.
create or replace view workout_routine_status as
select r.id,
       r.name,
       max(s.on_date) as last_done,
       count(s.id)    as done_count
  from routines r
  left join workout_sessions s
         on s.ended_at is not null
        and (s.routine_id = r.id or s.routine_name = r.name)
 group by r.id, r.name
 order by max(s.on_date) asc nulls first;

-- 최근 주간 부위별 볼륨. "이번 주에 하체 했나"의 답.
create or replace function volume_by_group(
  p_days int default 7,
  p_tz   text default 'Asia/Seoul'
) returns table (muscle_group text, volume_kg numeric)
language sql stable as $$
  select g.muscle_group, sum(g.volume_kg)
    from workout_group_volume g
   where g.on_date > (now() at time zone p_tz)::date - p_days
   group by g.muscle_group
   order by 2 desc
$$;

-- ── 3. 맥락 회수 ────────────────────────────────────────────────────────
-- 사람·기간으로 좁히고, 그 안에서 의미가 가까운 순으로.
--
-- 이것이 pgvector 를 고른 이유다. 이 한 번의 조회에 관계 조건(participants)과
-- 시간 조건과 벡터 거리가 같이 들어간다. 벡터를 다른 저장소에 두면 사람 id 와
-- 날짜를 양쪽에 중복해서 들고 있어야 하고, 그 동기화는 이 규모에서 순이익이
-- 아니다.
--
-- p_person 이 NULL 이면 사람 조건을 걸지 않는다. 운동·자산처럼 사람이 없는
-- 조각도 같은 인덱스에서 꺼내야 한다.
create or replace function recall(
  p_query   vector(1536),
  p_person  bigint  default null,
  p_days    int     default null,
  p_kinds   text[]  default null,
  p_limit   int     default 5
) returns table (
  id          bigint,
  kind        text,
  ref_table   text,
  ref_id      text,
  occurred_at timestamptz,
  body        text,
  distance    double precision
)
language sql stable as $$
  select m.id, m.kind, m.ref_table, m.ref_id, m.occurred_at, m.body,
         (m.embedding <=> p_query)::double precision
    from memory_chunks m
   where m.embedding is not null
     and (p_person is null or m.person_ids @> array[p_person])
     and (p_days   is null or m.occurred_at > now() - make_interval(days => p_days))
     and (p_kinds  is null or m.kind = any(p_kinds))
   order by m.embedding <=> p_query
   limit p_limit
$$;

-- 아직 결론이 없는 스레드. 벡터 없이도 답이 나오는 질문이라 별도로 둔다 -
-- "윤석이랑 얘기하던 그 건"은 대개 이 목록의 맨 위에 있다.
create or replace function open_threads(
  p_person bigint default null,
  p_days   int    default 30,
  p_limit  int    default 5
) returns setof threads
language sql stable as $$
  select * from threads
   where status = 'open'
     and (p_person is null or participants @> array[p_person])
     and (ended_at is null or ended_at > now() - make_interval(days => p_days))
   order by coalesce(ended_at, updated_at) desc
   limit p_limit
$$;
