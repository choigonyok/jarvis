-- 금현물 계좌. 한국투자증권 안에 있지만 주식 계좌와 별개라 원금도 따로 센다.
alter table principal_flows drop constraint if exists principal_flows_venue_check;
alter table principal_flows add constraint principal_flows_venue_check
  check (venue in ('upbit', 'kis', 'gold', 'other'));
