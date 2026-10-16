-- 자산 탭과 리밸런싱 승인 카드에서 낸 주문. assets-svc 가 증권사에 주문을 낸 그대로
-- 남긴다 - 무엇을 왜(reason), 어떤 계획의 일부로(plan_id), 증권사가 뭐라고 답했는지.
--
-- 원금 대비 수익률에는 들어가지 않는다: 원금은 입출금(principal_flows)만이고, 매매는
-- 증권사의 결제 내역으로 입출금 감지(kis-flows)에서 빠진다. 이 표는 그 계산이
-- 어긋났을 때 어떤 주문 때문인지 찾는 기록이다.
create table if not exists asset_orders (
  id          bigserial primary key,
  created_at  timestamptz not null default now(),
  venue       text not null check (venue in ('upbit', 'kis', 'gold')),
  symbol      text not null,
  side        text not null check (side in ('buy', 'sell')),
  -- 주식은 주 수, 코인은 매도 수량(시장가 매수는 금액으로 내므로 비어 있다).
  units       numeric,
  amount_krw  numeric not null,
  -- 지정가 주문의 가격, 그 종목의 통화로(미국 주식은 달러).
  limit_price numeric,
  currency    text not null default 'KRW' check (currency in ('KRW', 'USD')),
  reason      text not null default '',
  plan_id     text,
  -- placed: 증권사가 받음. failed: 거절되거나 닿지 않음. manual: 사람이 앱에서 낼 것(금현물).
  status      text not null check (status in ('placed', 'failed', 'manual')),
  broker_id   text,
  message     text not null default ''
);
create index if not exists asset_orders_created on asset_orders (created_at desc);

grant select, insert, update on asset_orders to svc_assets;
grant usage, select on sequence asset_orders_id_seq to svc_assets;
