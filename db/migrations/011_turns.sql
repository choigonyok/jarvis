-- 대화. 지금의 thread.json 이 옮겨올 자리다.
--
-- 스레드가 하나뿐인 제품이라 thread_id 컬럼이 없다. 여러 스레드가 필요해지면
-- 그때 추가하는 것이, 지금 있지도 않은 두 번째 스레드를 위해 모든 조회에
-- where 절을 하나 더 붙이는 것보다 낫다.
--
-- proposal_id 는 카드가 이 턴에서 열렸다는 기록일 뿐이다. 카드가 무엇을
-- 말하고 어떻게 됐는지는 proposals 의 일이다.
create table if not exists turns (
  id          text primary key,           -- 't-<epoch ms>-<seq>' 형식
  role        text not null check (role in ('agent','user')),
  at          timestamptz not null default now(),
  -- 문단 배열과 단일 텍스트를 둘 다 두는 것은 기존 구조를 그대로 옮긴 것이다:
  -- 에이전트는 문단으로 말하고 사람은 한 덩어리로 친다.
  paragraphs  text[],
  body        text,
  proposal_id text references proposals(id) on delete set null,
  -- 이 턴을 만든 CLI 세션. 재개(resume)할 때 필요하다.
  session_id  text
);

create index if not exists turns_at_idx on turns (at);
create index if not exists turns_proposal_idx
  on turns (proposal_id) where proposal_id is not null;
