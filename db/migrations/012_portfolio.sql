-- 자산. 다른 표들과 성격이 다르다.
--
-- 현재 보유는 업비트와 KIS 가 진실의 원천이고, 우리가 저장하는 것은 사본이다.
-- 그래서 이 표는 "지금 얼마인가"에 답하기 위한 것이 아니라 - 그건 매번
-- 증권사에 물어야 한다 - "어제와 비교해 얼마나 달라졌나"에 답하기 위한
-- 것이다.
--
-- 지금은 그 비교를 과거 종가로 현재 바스켓을 다시 매겨서 만든다
-- (web/src/lib/change.ts). 그 방식은 입출금을 수익률로 착각하지 않는 장점이
-- 있지만, 과거에 실제로 무엇을 들고 있었는지는 모른다. 스냅샷이 쌓이면
-- 그 답도 생긴다. 둘은 서로를 대체하지 않으므로 둘 다 남긴다.
create table if not exists portfolio_snapshots (
  id         bigserial primary key,
  taken_at   timestamptz not null default now(),
  -- 하루 한 장이 기준선이다. 장중에 여러 번 찍히면 일별 비교의 기준이
  -- 흔들리므로, 같은 날짜의 재실행은 덮어쓴다.
  on_date    date not null,
  total_krw  numeric(18,2) not null,
  cash_krw   numeric(18,2) not null default 0,
  cost_krw   numeric(18,2) not null default 0,
  usd_krw    numeric(10,4),
  -- 실패한 거래소는 이름을 남긴다. 조용히 빠진 보유가 있는 총액은 틀린
  -- 총액이고, 틀렸다는 말이 어디에도 없는 것이 가장 나쁘다.
  problems   text[] not null default '{}',
  unique (on_date)
);

create table if not exists portfolio_holdings (
  id          bigserial primary key,
  snapshot_id bigint not null references portfolio_snapshots(id) on delete cascade,
  venue       text not null check (venue in ('upbit','kis')),
  kind        text not null check (kind in ('coin','stock')),
  symbol      text not null,
  name        text,
  quantity    numeric(28,10) not null,
  avg_price   numeric(18,4),
  price       numeric(18,4),
  currency    text not null check (currency in ('KRW','USD')),
  value_krw   numeric(18,2) not null,
  cost_krw    numeric(18,2) not null default 0,
  unique (snapshot_id, venue, symbol)
);

create index if not exists portfolio_snapshots_date_idx
  on portfolio_snapshots (on_date desc);
create index if not exists portfolio_holdings_symbol_idx
  on portfolio_holdings (venue, symbol);

-- 하루 한 장. 같은 날 다시 찍으면 그날의 장을 갈아끼운다.
create or replace function upsert_snapshot(
  p_on_date   date,
  p_total_krw numeric,
  p_cash_krw  numeric,
  p_cost_krw  numeric,
  p_usd_krw   numeric,
  p_problems  text[]
) returns bigint
language plpgsql as $$
declare
  snap_id bigint;
begin
  insert into portfolio_snapshots
    (on_date, total_krw, cash_krw, cost_krw, usd_krw, problems)
  values
    (p_on_date, p_total_krw, p_cash_krw, p_cost_krw, p_usd_krw,
     coalesce(p_problems, '{}'))
  on conflict (on_date) do update set
    taken_at  = now(),
    total_krw = excluded.total_krw,
    cash_krw  = excluded.cash_krw,
    cost_krw  = excluded.cost_krw,
    usd_krw   = excluded.usd_krw,
    problems  = excluded.problems
  returning id into snap_id;

  -- 보유는 갈아끼운다. 어제 팔아치운 종목이 오늘 장에 남아 있으면 그 스냅샷은
  -- 어느 날의 것도 아니게 된다.
  delete from portfolio_holdings where snapshot_id = snap_id;
  return snap_id;
end $$;
