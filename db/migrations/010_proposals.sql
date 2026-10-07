-- 제안. 이 시스템의 심장이 영속화되는 자리다.
--
-- 기존 core/proposal 의 필드를 그대로 받고, 선제 제안에 필요한 것들을 더한다:
--   trigger_kind  무엇을 보고 열었는지 (룰 게이트의 이름)
--   suppress_key  같은 종류의 제안을 다시 올리지 않기 위한 열쇠
--   expires_at    답이 없으면 스스로 닫히는 시각
--   urgency       지금 말할지 저녁에 말할지
--   reason        왜 이 제안이 나왔는지. 사람이 읽고, 튜닝할 때 근거가 된다
--
-- created_at 이 실제 timestamptz 인 것이 기존 구조와의 차이다. 지금은 'at' 이
-- 날짜 없는 벽시계("15:04")라서 프론트가 id 에 박힌 epoch 를 파싱해 날짜를
-- 복원하고 있다(web/src/lib/ledger.ts 의 raisedAt). 그 우회는 이 컬럼이
-- 채워지면 지워도 된다.
create table if not exists proposals (
  id           text primary key,          -- 'p-<epoch ms>-<seq>' 형식 유지
  origin       text not null,             -- 'chat' | 'intercept' | 'proactive'
  trigger_kind text,                      -- 'calendar.conflict' | 'workout.gap'
  suppress_key text,
  action       jsonb,                     -- core/action.Action
  card         jsonb,                     -- core/action.Card. 모듈이 그린 것
  message      text,                      -- 사람에게 보낸 문장
  reason       text,
  confidence   double precision,          -- 직접 지시한 일은 0
  urgency      int not null default 0,
  state        text not null default 'pending'
                 check (state in ('pending','approved','rejected','executed','failed')),
  note         text,
  created_at   timestamptz not null default now(),
  expires_at   timestamptz,
  decided_at   timestamptz,
  -- 어느 이벤트에서 비롯됐는지, 그리고 어느 추론에서 나왔는지. 이 둘이 없으면
  -- "왜 이 제안이 나왔나"를 역추적할 수 없고, 역추적이 안 되면 튜닝이 안 된다.
  source_event_id bigint references raw_events(id),
  trace_id     text
);

create index if not exists proposals_created_idx on proposals (created_at desc);
create index if not exists proposals_pending_idx
  on proposals (created_at desc) where state = 'pending';
-- 쿨다운 조회: "이 열쇠로 마지막에 제안한 게 언제였나".
create index if not exists proposals_suppress_idx
  on proposals (suppress_key, created_at desc) where suppress_key is not null;

-- 결정의 기록. proposals.state 가 현재 상태라면 이쪽은 이력이고, 학습에 쓰는
-- 것은 이쪽이다.
--
-- 'ignored' 가 별도 값인 이유: 반려와 무응답은 다른 신호다. 반려는 "그건
-- 아니다"이고 무응답은 "지금은 아니다"에 가깝다. 둘을 합치면 시각대만 옮기면
-- 될 제안을 영구히 억제하게 된다.
create table if not exists proposal_feedback (
  id          bigserial primary key,
  proposal_id text not null references proposals(id) on delete cascade,
  decision    text not null
                check (decision in ('approved','rejected','ignored','expired')),
  at          timestamptz not null default now(),
  note        text
);

create index if not exists proposal_feedback_proposal_idx
  on proposal_feedback (proposal_id, at desc);

-- 이 열쇠가 쿨다운 안에 있나. 룰 게이트가 LLM 을 부르기 전에 묻는다.
--
-- 반려·무응답만 세지 않고 승인까지 세는 이유: 승인된 제안도 다시 올릴 일은
-- 없다. 이미 처리된 일을 또 제안하는 것이 가장 빠르게 신뢰를 깎는다.
create or replace function suppressed(p_key text, p_window interval)
returns boolean
language sql stable as $$
  select exists (
    select 1 from proposals
     where suppress_key = p_key
       and created_at > now() - p_window
  )
$$;

-- 오늘 이미 몇 건 올렸나. 일일 상한을 지키기 위한 것.
-- 사람이 직접 시킨 일('chat')은 세지 않는다 - 상한은 내가 말을 거는 횟수를
-- 제한하려는 것이고, 시켜서 한 일은 소음이 아니다.
create or replace function proposals_today(p_tz text default 'Asia/Seoul')
returns bigint
language sql stable as $$
  select count(*) from proposals
   where origin <> 'chat'
     and (created_at at time zone p_tz)::date = (now() at time zone p_tz)::date
$$;
