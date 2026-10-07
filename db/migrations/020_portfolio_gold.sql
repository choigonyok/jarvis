-- 금현물 계좌가 스냅샷에 들어올 수 있게 한다. 거래소도 종류도 하나씩 늘었다.
alter table portfolio_holdings drop constraint if exists portfolio_holdings_kind_check;
alter table portfolio_holdings add constraint portfolio_holdings_kind_check
  check (kind in ('coin', 'stock', 'gold'));
alter table portfolio_holdings drop constraint if exists portfolio_holdings_venue_check;
alter table portfolio_holdings add constraint portfolio_holdings_venue_check
  check (venue in ('upbit', 'kis', 'gold'));
