-- 대기 주문: "내려오면 사" 같은 지정가 주문. 미국 주식 지정가 주문은 그날 장이 끝나면
-- 사라지므로, assets-svc 가 체결될 때까지 거래일마다 같은 주문(사람이 정한 그 가격·수량)을
-- 다시 낸다. 체결은 보유 수량으로 본다: 만들 때 갖고 있던 수량(held_before)에서 units 만큼
-- 늘면(매도는 줄면) 체결된 것이다.
create table if not exists standing_orders (
  id          bigserial primary key,
  created_at  timestamptz not null default now(),
  venue       text not null check (venue in ('kis')),
  symbol      text not null,
  side        text not null check (side in ('buy', 'sell')),
  units       integer not null check (units > 0),
  -- 지정가, 달러.
  limit_price numeric not null check (limit_price > 0),
  held_before numeric not null,
  reason      text not null default '',
  status      text not null default 'active' check (status in ('active', 'filled', 'cancelled', 'expired')),
  -- 마지막으로 낸 거래일(뉴욕 날짜)과 증권사 주문번호. 같은 날은 한 번만 낸다.
  placed_on   date,
  broker_id   text,
  message     text not null default '',
  -- 이 날(뉴욕 날짜)이 지나면 그만 낸다. 없으면 취소할 때까지.
  expires_on  date,
  done_at     timestamptz
);
create index if not exists standing_orders_active on standing_orders (status) where status = 'active';

grant select, insert, update on standing_orders to svc_assets;
grant usage, select on sequence standing_orders_id_seq to svc_assets;
