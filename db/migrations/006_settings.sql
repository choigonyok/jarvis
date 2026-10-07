-- 기능별 설정 한 칸씩. 운동의 restTarget 처럼 값이 하나뿐인 것들이 갈 자리다.
--
-- 세션 테이블에 넣지 않은 이유: 휴식 목표는 세션의 속성이 아니라 사람의
-- 설정이고, 세션마다 복사해두면 과거 세션을 볼 때마다 그때의 설정이
-- 되살아난다. 반대로 서비스마다 설정 테이블을 따로 만들면 같은 모양의 표가
-- 네 개 생긴다.
create table if not exists settings (
  scope      text not null,          -- 'workout' | 'assets' | 'chat'
  key        text not null,
  value      jsonb not null,
  updated_at timestamptz not null default now(),
  primary key (scope, key)
);

create or replace function set_setting(p_scope text, p_key text, p_value jsonb)
returns void
language sql as $$
  insert into settings (scope, key, value) values (p_scope, p_key, p_value)
  on conflict (scope, key)
  do update set value = excluded.value, updated_at = now()
$$;
