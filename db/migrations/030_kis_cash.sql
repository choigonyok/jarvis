-- 한국투자증권은 입출금 내역 API 가 없다. 대신 매매마다 결제일과 정산금액을
-- 알려주므로, 정산된 현금을 매일 적어 두면 "현금 증감 - 그 사이
-- 결제된 매매"가 곧 그날 들어오거나 나간 돈이다.
--
-- 통화는 따로 적는다. 판 돈은 달러로 들어와 손으로 환전할 때까지 달러로
-- 남으므로, 둘을 그날 환율로 합쳐 두면 환율이 움직일 때마다 입출금처럼 보인다.
--
-- 금현물 계좌도 같은 방식이다(원화뿐이라 달러 칸은 0). 계좌마다 하루에 한
-- 줄, 그날 마지막으로 본 값으로 덮어쓴다.
create table if not exists kis_cash_daily (
  venue       text not null check (venue in ('kis', 'gold')),
  on_date     date not null,
  -- 정산된 예수금. 미결제 매매는 넣지 않는다.
  cash_krw    numeric(18,2) not null,
  cash_usd    numeric(18,4) not null default 0,
  -- 원금 기준일부터 그날까지 결제된 매매의 순 금액(매도 +, 매수 -, 수수료 포함).
  -- 주식 계좌는 달러로 결제되고, 금현물 계좌는 원화로 결제된다.
  settled_krw numeric(18,2) not null default 0,
  settled_usd numeric(18,4) not null default 0,
  observed_at timestamptz not null default now(),
  primary key (venue, on_date)
);

-- 위 계산으로 찾은 입출금은 원금 기록에 'auto' 로 들어간다. 손으로 적은
-- 기록과 구별해야 하고(지우기는 손 기록만), 하루에 하나라 다시 계산할 때
-- 덮어쓴다.
alter table principal_flows
  add column if not exists source text not null default 'manual';
alter table principal_flows drop constraint if exists principal_flows_source_check;
alter table principal_flows add constraint principal_flows_source_check
  check (source in ('manual', 'auto'));
create unique index if not exists principal_flows_auto_day
  on principal_flows (venue, on_date) where source = 'auto';
