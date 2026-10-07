-- 묶음 가맹점의 상품 단위 내역. 카드 알림에는 "쿠팡 8,900원"까지만 있고, 무엇을
-- 샀는지는 쿠팡 주문목록에 있다. 에이전트가 백그라운드에서 그 목록을 읽어 여기에 적는다.
--
-- 카드 내역 자체는 고치지 않는다. 상품 줄은 그 아래에 붙고, 합계는 여전히 카드
-- 금액이다 - 상품 금액은 카드 금액을 나눠 가진 것(amount_krw)이고, 주문서에 적힌
-- 가격(listed_krw)과 다를 수 있다(쿠폰, 캐시, 배송비).
create table if not exists card_transaction_items (
  id            bigserial primary key,
  tx_id         bigint not null references card_transactions(id) on delete cascade,
  name          text not null,
  quantity      int not null default 1 check (quantity > 0),
  listed_krw    bigint not null check (listed_krw >= 0),
  amount_krw    bigint not null,
  category      text not null,
  -- 'agent' 는 에이전트가 고른 분류, 'manual' 은 사람이 고친 것.
  category_source text not null default 'agent' check (category_source in ('agent', 'manual')),
  created_at    timestamptz not null default now()
);

create index if not exists card_transaction_items_tx_idx on card_transaction_items (tx_id);

-- 조회 상태. NULL 은 대상이 아님.
--   pending         조회할 차례를 기다림 (enrich_next_at 이후)
--   done            상품을 적음
--   not_found       몇 번 찾아도 주문이 없었음
--   login_required  그 사이트에 다시 로그인해야 함 - 같은 사이트의 대기 건은 모두 멈춘다
alter table card_transactions
  add column if not exists enrich_source   text,   -- 'coupang' | 'naverpay'
  add column if not exists enrich_status   text
    check (enrich_status in ('pending', 'done', 'not_found', 'login_required')),
  add column if not exists enrich_attempts int not null default 0,
  add column if not exists enrich_next_at  timestamptz,
  add column if not exists enrich_note     text not null default '';

create index if not exists card_transactions_enrich_idx
  on card_transactions (enrich_status, enrich_next_at)
  where enrich_status is not null;

-- 이미 들어와 있는 묶음 가맹점 결제도 대상으로. 30일보다 오래된 것은 주문목록에서
-- 찾기 어렵고, 지금 와서 쪼갤 이유도 적다.
update card_transactions
   set enrich_source = case when upper(merchant) like '%COUPANG%' or merchant like '쿠팡%' then 'coupang'
                            else 'naverpay' end,
       enrich_status = 'pending',
       enrich_next_at = now()
 where enrich_status is null
   and kind = 'approval'
   and approved_at > now() - interval '30 days'
   and merchant not like '쿠팡이츠%'
   and (merchant like '쿠팡%' or upper(merchant) like 'COUPANG%'
        or merchant like '네이버페이%' or upper(merchant) like 'NAVERPAY%');
