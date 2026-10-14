-- 회고와 선호. 답 하나에는 "대상의 속성"(OO캠핑장은 시끄럽다)과 "나의 선호"(나는 소음에 민감하다)가
-- 섞여 있어 층을 나눈다.
--
-- 관찰: 회고 카드의 답을 해석 없이 그대로(대상, 차원, 방향, 그때의 맥락). 지우지 않는다 - 차원을
-- 바꿔도 여기서 다시 계산한다.
create table if not exists mind_observations (
  id           bigserial primary key,
  subject_type text not null,              -- 캠핑장 | 식당 | 숙소 | 카페
  subject_id   text not null,              -- 대상의 이름
  dimension    text not null,              -- 소음 | 청결 | 가격 대비 | … | 기타 | '' (총점만)
  polarity     smallint not null default 0, -- +1 좋았음 | -1 아쉬웠음 | 0
  score        smallint not null default 0, -- 그 방문의 총점 1~5
  detail       text not null default '',   -- 기타의 자유 서술
  context      jsonb not null default '{}', -- 동행, 계절, 요일
  source_id    text not null,              -- 회고 의도(int_…)
  at           timestamptz not null
);
create index if not exists mind_observations_subject on mind_observations (subject_type, subject_id);
create index if not exists mind_observations_dimension on mind_observations (dimension, polarity);

-- 속성: 대상마다 차원의 평균 방향과 근거 수. 관찰에서 바로 계산되는 보기(view)라 따로 맞출 것이 없다.
create or replace view mind_attributes as
  select subject_type, subject_id, dimension, avg(polarity)::real as value, count(*) as evidence_count
    from mind_observations where dimension <> '' group by subject_type, subject_id, dimension;

-- 선호: 서로 다른 대상 여럿에서 같은 차원·방향이 나와야 추정이 되고, 사람이 확인하면 확정, 아니라고 하면
-- 거절(다시 묻지 않는다). 직접 한 말("원래 시끄러운 거 싫어")은 바로 확정.
create table if not exists mind_preferences (
  dimension  text not null,
  polarity   smallint not null,
  scope      text not null default '',     -- '' 늘 | 가족과 갈 때 …
  status     text not null,                -- estimated | confirmed | rejected
  body       jsonb not null,               -- model.Preference 전체(근거 대상, 반대 근거, 신뢰도)
  updated_at timestamptz not null,
  primary key (dimension, polarity, scope)
);
