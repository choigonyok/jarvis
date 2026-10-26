-- 투자 계좌에서 나간 돈이 어디로 갔나. 사람이 답한 것만 남긴다.
--
-- 다른 투자 계좌로 다시 들어간 출금(옮김)은 assets-svc 가 조회할 때마다 짝을 찾아
-- 계산하므로 여기 없다(outflows.ts). 짝이 없는 출금은 자산 카드로 묻고, 그 답이 이
-- 표다. 원금·수익률은 이 표와 상관없다 - 뺀 돈은 어디에 썼든 이미 원금에서 빠졌다.
--
-- 출금은 행 id 가 아니라 고정 키로 잇는다. 업비트 출금은 이 데이터베이스에 행이 없고
-- (API 에서 읽는다), 한투·금 자동 감지 행은 다시 계산되며 지워졌다 생기면 id 가
-- 바뀐다: upbit:<uuid> | manual:<principal_flows.id> | auto:<계좌>:<YYYY-MM-DD>.
create table if not exists outflow_uses (
  flow_key   text not null,
  -- 답할 때 본 출금액(양수). 출금액이 바뀌면 답은 낡은 것으로 보고 다시 묻는다.
  flow_krw   numeric(18,2) not null check (flow_krw > 0),
  -- spend: 계좌이체·현금으로 썼다(가계부에 들어간다). card: 이미 가계부에 있는 카드
  -- 결제를 메웠다. saved: 예금·청약처럼 다른 곳에 두었다. parked: 아직 통장에 있다(다시
  -- 넣을 돈 - 30일 안에 같은 금액이 투자 계좌로 들어오면 옮김으로 보고, 아니면 다시
  -- 묻는다). transfer: 다른 투자 계좌로 옮겼다(짝을 자동으로 못 찾은 것을 사람이 말함).
  use        text not null check (use in ('spend', 'card', 'saved', 'parked', 'transfer')),
  -- 그 용도로 간 몫. 한 출금을 여러 용도로 나눠 답할 수 있다.
  amount_krw numeric(18,2) not null check (amount_krw > 0),
  -- spend 일 때 고른 가계부 분류. 가계부에 들어간 뒤로 분류는 가계부의 것이다.
  category   text,
  -- 어디에 썼는지 한마디("월세"). spend 면 가계부의 사용처가 된다.
  memo       text not null default '',
  decided_at timestamptz not null default now(),
  primary key (flow_key, use)
);

grant select, insert, update, delete on outflow_uses to svc_assets;

-- 가계부: 'spend' 로 답한 몫은 spending-svc 가 assets-svc 에서 읽어 와 한 줄로 넣는다
-- (source 'asset', source_ref 'asset:<flow_key>@<답한 시각>'). 답이 바뀌면 그 줄은
-- 지워지고 새 줄이 들어간다 - 가계부에서 지울 수는 없고, 자산 탭에서 다시 묻는다.
alter table card_transactions drop constraint if exists card_transactions_source_check;
alter table card_transactions add constraint card_transactions_source_check
  check (source in ('kakao', 'manual', 'asset'));
