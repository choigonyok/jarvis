-- 원금. 수익을 "지금 평가액 - 매입가"가 아니라 "지금 총자산 - 넣은 돈"으로
-- 재려면 넣고 뺀 돈의 기록이 있어야 한다.
--
-- 업비트는 입출금 내역 API 가 있어 assets-svc 가 직접 읽는다. 여기 쌓이는
-- 것은 증권사가 알려주지 않는 것들이다: 한국투자증권 입출금(API 없음)과
-- 기준일 시점에 이미 들고 있던 자산(시작 원금).
create table if not exists principal_flows (
  id         bigserial primary key,
  on_date    date not null,
  venue      text not null check (venue in ('upbit', 'kis', 'other')),
  -- 넣은 돈은 +, 뺀 돈은 -. 부호 하나로 방향을 적어 합계가 곧 원금이다.
  amount_krw numeric(18,2) not null check (amount_krw <> 0),
  memo       text not null default '',
  created_at timestamptz not null default now()
);

create index if not exists principal_flows_on_date on principal_flows (on_date);
