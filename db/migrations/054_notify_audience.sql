-- 알림의 받는 사람. 지금까지 알림은 운영자 한 사람의 것이었다. 손님 계정(파트너)도
-- 자기 것만 받는다: 손님 jarvis 의 결재 카드, 운영자 쪽에서 더한 공유 일정. 운영자
-- 전용(위치·자산·가계부·상태)은 손님에게 가지 않는다 - 보내는 쪽이 audience 를 정하고,
-- 콘솔은 로그인한 역할의 것만 읽는다(web 의 /api/notify 가 역할로 정해 넘긴다).
alter table notifications
  add column if not exists audience text not null default 'owner' check (audience in ('owner', 'guest'));
create index if not exists notifications_audience_created on notifications (audience, created_at desc);

-- 기기도 누구의 것인지. 같은 브라우저에서 다른 사람으로 다시 구독하면 바뀐다.
alter table push_subscriptions
  add column if not exists audience text not null default 'owner' check (audience in ('owner', 'guest'));

-- 설정(꺼 둔 분류, 방해 금지, 요약 시각)도 사람마다 한 줄. 운영자의 것은 031 의
-- notify_settings 에서 옮겨 온다. 그 표는 이제 읽지 않는다.
create table if not exists notify_audience_settings (
  audience   text primary key check (audience in ('owner', 'guest')),
  settings   jsonb not null default '{}',
  updated_at timestamptz not null default now()
);
insert into notify_audience_settings (audience, settings, updated_at)
  select 'owner', settings, updated_at from notify_settings where id
  on conflict (audience) do nothing;

grant select, insert, update, delete on notify_audience_settings to svc_notify;
