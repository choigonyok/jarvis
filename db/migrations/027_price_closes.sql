-- 종가 이력. 지난 날의 종가는 바뀌지 않으므로 한 번 받으면 계속 쓴다 - 자산 탭이
-- 열릴 때마다 증권사에 400일을 다시 묻던 것이 KIS 초당 거래건수 초과의 원인이었다.
-- key 는 보유의 id("kis:KO", "upbit:BTC", "gold:M04020000"). 오늘 행은 장중 값일 수
-- 있어 다음 조회 때 덮어쓴다.
create table if not exists price_closes (
  key      text not null,
  on_date  date not null,
  close    numeric(24, 8) not null,
  primary key (key, on_date)
);
