-- 나에 대한 노트, 예측, 기대. 노트는 사람이 읽고 고칠 수 있는 문장이고(가설 → 확정/기각),
-- 고칠 때마다 버전과 이유를 남긴다. 예측은 "오늘 운동 65%"처럼 확률과 기한을 갖고, 기한이
-- 지나면 실제와 대조해 채점한다(Brier). 기대는 예측이 기다리는 발생을 키로 찾게 하는 색인이다:
-- 새 발생은 열린 기대를 키로만 대조한다(과거를 훑지 않는다).
create table if not exists mind_notes (
  id         text primary key,
  status     text not null,               -- hypothesis | confirmed | rejected
  scope      text not null default '',
  body       jsonb not null,               -- model.Note 전체(문장, 근거 ref, 신뢰도, 규칙)
  version    integer not null,
  updated_at timestamptz not null
);

create table if not exists mind_note_versions (
  note_id text not null,
  version integer not null,
  body    jsonb not null,                  -- 그 버전의 문장·상태·신뢰도와 바꾼 이유
  at      timestamptz not null,
  primary key (note_id, version)
);

create table if not exists mind_predictions (
  id         text primary key,
  target     text not null,               -- workout:2026-10-15, buy:장작:plan_x …
  due        timestamptz not null,
  scored     boolean not null default false,
  body       jsonb not null,               -- model.Prediction 전체(확률, 근거 노트, 결과, 점수)
  created_at timestamptz not null
);
create index if not exists mind_predictions_due on mind_predictions (due) where not scored;
create index if not exists mind_predictions_target on mind_predictions (target);

create table if not exists mind_expectations (
  prediction_id text not null,
  match_key     text not null,            -- purchase.item:장작, workout, transfer:민수 …
  due           timestamptz not null,
  state         text not null,             -- open | met | missed
  body          jsonb not null,
  primary key (prediction_id, match_key)
);
create index if not exists mind_expectations_open on mind_expectations (match_key) where state = 'open';
