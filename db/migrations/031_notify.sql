-- 알림. 여러 서비스가 notify-svc 에 정해진 모양의 이벤트를 보내고, notify-svc
-- 가 저장·중복 제거·방해 금지·하루 요약·웹 푸시를 맡는다. 알림함은 이 표를
-- 그대로 읽는다.
create table if not exists notifications (
  id           bigserial primary key,
  source       text not null,               -- 보낸 서비스: agent, status, spending, assets, jobs, calendar ...
  kind         text not null,               -- '분류.세부', 예: collect.fail, spending.big, assets.band
  tier         text not null check (tier in ('now', 'digest', 'log')),
  level        text not null default 'info' check (level in ('info', 'warn', 'alert')),
  title        text not null,
  body         text not null default '',
  url          text not null default '/',
  -- 같은 일을 두 번 알리지 않게, 보낸 쪽이 정하는 키. 없으면 매번 새 알림이다.
  dedupe_key   text unique,
  created_at   timestamptz not null default now(),
  -- 푸시로 나간(또는 꺼진 분류라 보내지 않기로 한) 때. null 이면 아직 대기 중:
  -- 요약을 기다리거나, 방해 금지 시간이 끝나기를 기다린다.
  delivered_at timestamptz,
  read_at      timestamptz
);
create index if not exists notifications_created on notifications (created_at desc);
create index if not exists notifications_pending on notifications (tier, delivered_at) where delivered_at is null;

-- 웹 푸시 구독. 기기(브라우저)마다 하나.
create table if not exists push_subscriptions (
  endpoint    text primary key,
  p256dh      text not null,
  auth        text not null,
  user_agent  text not null default '',
  created_at  timestamptz not null default now(),
  last_ok_at  timestamptz,
  failures    integer not null default 0
);

-- 설정은 한 줄. 꺼 둔 분류, 방해 금지 시간, 요약 시각.
create table if not exists notify_settings (
  id        boolean primary key default true check (id),
  settings  jsonb not null default '{}',
  updated_at timestamptz not null default now()
);
insert into notify_settings (id) values (true) on conflict do nothing;
