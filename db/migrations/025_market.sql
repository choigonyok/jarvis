-- 대화에 붙인 사진. 파일은 에이전트의 /uploads 볼륨에 있고, 여기에는 그 이름만 적는다.
alter table turns add column if not exists images text[];

-- 중고나라 판매 글. 대화에서 승인한 초안이 여기에 'queued' 로 들어오고, 에이전트가
-- 백그라운드에서 브라우저로 등록한 뒤 중고나라의 글 번호를 적는다.
--
-- 중고나라가 원본이다. status·가격·조회수는 그쪽 "내 상점" 화면을 주기적으로 읽어
-- 맞춘다. 여기서 바꾸는 것은 언제나 market_tasks 를 거쳐 중고나라에 먼저 반영된다.
create table if not exists market_listings (
  id               bigserial primary key,
  -- queued   등록 대기 (작업이 남아 있음)
  -- active   판매중 / reserved 예약중 / sold 판매완료
  -- deleted  중고나라에서 지움 / failed 등록하지 못함
  status           text not null default 'queued'
                   check (status in ('queued', 'active', 'reserved', 'sold', 'deleted', 'failed')),
  title            text not null,
  price_krw        bigint not null check (price_krw >= 0),
  description      text not null default '',
  category         text not null default '',
  condition        text not null default '',
  -- 택배비: 'included' 포함, 'separate' 별도
  shipping         text not null default 'included' check (shipping in ('included', 'separate')),
  photos           text[] not null default '{}',
  joongna_id       text unique,
  url              text not null default '',
  views            int not null default 0,
  likes            int not null default 0,
  chats            int not null default 0,
  note             text not null default '',
  posted_at        timestamptz,
  sold_at          timestamptz,
  synced_at        timestamptz,
  -- 판매가 끝나고 30일이 지나면 사진 파일을 지운다. 지운 때를 적어 두 번 지우지 않는다.
  photos_purged_at timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create index if not exists market_listings_status_idx on market_listings (status);

-- 중고나라에 반영할 일. 하나씩, 오래된 것부터 처리한다.
create table if not exists market_tasks (
  id          bigserial primary key,
  listing_id  bigint not null references market_listings(id) on delete cascade,
  -- post 등록 / price 가격 변경 / status 상태 변경 / bump 끌어올리기 / delete 삭제
  kind        text not null check (kind in ('post', 'price', 'status', 'bump', 'delete')),
  price_krw   bigint,
  -- kind = 'status' 일 때 바꿀 상태
  to_status   text check (to_status in ('active', 'reserved', 'sold')),
  state       text not null default 'pending' check (state in ('pending', 'done', 'failed', 'cancelled')),
  attempts    int not null default 0,
  next_at     timestamptz not null default now(),
  note        text not null default '',
  created_at  timestamptz not null default now(),
  done_at     timestamptz
);

create index if not exists market_tasks_pending_idx on market_tasks (state, next_at) where state = 'pending';

-- 동기화 상태 한 줄. login_required 가 서 있으면 모든 작업이 멈추고, 사람이 다시
-- 로그인한 뒤 탭에서 풀 때까지 기다린다.
create table if not exists market_state (
  id              int primary key default 1 check (id = 1),
  login_required  boolean not null default false,
  login_note      text not null default '',
  sync_requested  boolean not null default false,
  last_sync_at    timestamptz,
  last_sync_note  text not null default ''
);
insert into market_state (id) values (1) on conflict do nothing;
