-- 같은 날 두 번 본 사이에 원화가 줄고 달러가 늘었으면(또는 그 반대) 환전이다. 그날의 입출금은
-- 환전을 빼고 센다: 환전으로 생긴 달러는 넣은 돈이 아니고, 환전에 쓴 원화는 뺀 돈이 아니다.
-- 그날 지금까지 본 환전의 합(원화로 바꾼 쪽은 음수)을 그날 줄에 둔다. 그날 처음 본 때에는 0.
-- (2026-10-28: 13만 원을 넣고 같은 밤 달러로 바꾼 것이, 그날 마지막 조회의 달러를 그날 환율로
-- 값 매겨 입금 127,745원으로 적혔다 - 환전 때 붙은 환율 차이가 입금에서 빠졌다.)
alter table kis_cash_daily add column if not exists exch_krw numeric(18,2) not null default 0;
alter table kis_cash_daily add column if not exists exch_usd numeric(18,4) not null default 0;
