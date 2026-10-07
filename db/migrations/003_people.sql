-- 사람. 제안의 절반은 "누구와"에 걸려 있어서, 표기가 흔들리는 것을 한 곳에서
-- 흡수해야 한다. 카톡 표시이름은 상대가 언제든 바꿀 수 있고, 캘린더에는
-- 본명이 적히고, 대화에서는 별명으로 부른다.
create table if not exists people (
  id         bigserial primary key,
  name       text not null,              -- 정규 표기. 우리가 부르기로 정한 이름
  note       text,
  created_at timestamptz not null default now()
);

-- 대소문자만 다른 두 사람을 만들 여지를 없앤다.
create unique index if not exists people_name_key on people (lower(name));

-- 별명 → 사람. 소스를 키에 넣은 이유는 같은 표기가 소스마다 다른 사람을
-- 가리킬 수 있기 때문이다. 카톡의 "형"과 캘린더의 "형"은 같은 사람이 아니다.
create table if not exists person_aliases (
  source    text   not null default '*',  -- '*' = 모든 소스에서 통하는 별명
  alias     text   not null,
  person_id bigint not null references people(id) on delete cascade,
  primary key (source, alias)
);

create index if not exists person_aliases_person_idx on person_aliases (person_id);

-- 새 표기가 나타났을 때 "혹시 이 사람인가"를 묻기 위한 것. 자동 병합에는
-- 쓰지 않는다 - 사람을 합치는 것은 사람이 결정한다.
create index if not exists person_aliases_alias_trgm
  on person_aliases using gin (alias gin_trgm_ops);

-- 별명이든 정규 표기든 하나로 찾는다. 못 찾으면 NULL 이고, 그때 새 사람을
-- 만들지 말지는 호출하는 쪽이 정한다.
-- 우선순위: 소스 전용 별명 > 모든 소스에서 통하는 별명 > 정규 표기.
-- 소스 전용이 먼저인 이유는 그게 더 좁은 주장이기 때문이다.
create or replace function person_by_alias(p_alias text, p_source text default '*')
returns bigint
language sql stable as $$
  select person_id from (
    select person_id, (source = p_source)::int as rank
      from person_aliases
     where alias = p_alias and source in (p_source, '*')
    union all
    select id, -1 from people where lower(name) = lower(p_alias)
  ) hit
  order by rank desc
  limit 1
$$;
