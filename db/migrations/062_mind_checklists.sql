-- 체크리스트(mind-svc internal/checklist). 캠핑 준비물, 집에 가서 할 일처럼 항목마다 했는지를
-- 남기는 목록이다. 대화(에이전트의 체크리스트 도구)가 만들고 고치며, 대화 카드에서 체크한 것도
-- 여기에 남는다. 리마인드(mind_reminders)가 목록을 하나 들고 있으면 그 때가 왔을 때 카드가
-- 그 목록을 지금 상태 그대로 보여 준다. 목록 하나가 body 한 덩어리(JSON)이고, 찾는 데 쓰는
-- 칸만 따로 둔다.
create table if not exists mind_checklists (
  id         text primary key,
  status     text not null default 'active' check (status in ('active', 'archived')),
  -- 같은 주제의 목록은 하나만 열려 있다(다시 만들면 그 목록에 더한다). 빈 값이면 주제 없음.
  topic      text not null default '',
  body       jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists mind_checklists_active on mind_checklists (status) where status = 'active';
create index if not exists mind_checklists_updated on mind_checklists (updated_at desc);

grant select, insert, update, delete on mind_checklists to svc_mind;
