-- 관문이 (스킬, 맥락 키)별로 배우는 승인율. 카드를 몇 번 보였고 몇 번 승인·거절·
-- 되돌렸는지, 마지막 결과가 무엇이었는지. 원본은 mind_intents 이고 이 표는 그 집계다:
-- 판단할 때마다 의도 이력 전체를 다시 세지 않으려고 둔다. 언제든
-- POST /api/policy/rebuild 로 mind_intents 에서 다시 만든다.
create table if not exists mind_policy_stats (
  skill       text not null,
  context_key text not null,
  shown       integer not null default 0,
  approved    integer not null default 0,
  rejected    integer not null default 0,
  undone      integer not null default 0,
  last        text not null default '',     -- approved | rejected | undone | 마지막 결과
  updated_at  timestamptz not null default now(),
  primary key (skill, context_key)
);
