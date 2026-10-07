-- 가계부. 카드사가 카톡으로 보내는 결제 알림이 원천이다.
--
-- 알림은 "승인"이지 "청구"가 아니다. 해외 결제의 원화 금액은 나중에 확정되고,
-- 취소는 별도 메시지로 온다. 그래서 이 표는 카드사 명세서의 사본이 아니라
-- "내가 언제 어디서 얼마를 썼다고 들었나"의 기록이고, 사람이 고칠 수 있다.
-- 원문은 raw_events 에 그대로 남으므로, 고친 값과 들은 값이 섞이지 않는다.
create table if not exists card_transactions (
  id            bigserial primary key,
  -- 'kakao' = 알림에서 읽음, 'manual' = 손으로 적음(현금, 놓친 알림)
  source        text not null check (source in ('kakao', 'manual')),
  -- 카톡 message_id. 수집을 몇 번 되감아도 같은 알림이 두 줄이 되지 않는 근거.
  source_ref    text unique,
  source_event_id bigint references raw_events(id),
  issuer        text not null default '',        -- '신한카드'
  card_tail     text not null default '',        -- 끝 네 자리. 모르면 빈 문자열
  kind          text not null default 'approval' check (kind in ('approval', 'cancel')),
  -- 원화. 해외 결제는 알림 시점 환율로 추정한 값이고 estimated 가 true 다.
  amount_krw    bigint not null check (amount_krw >= 0),
  currency      text not null default 'KRW',
  foreign_amount numeric(14, 2),
  estimated     boolean not null default false,
  installment   int not null default 0,          -- 0 = 일시불
  merchant      text not null,
  category      text not null default '기타',
  -- 분류를 누가 정했나. 'rule' 은 키워드, 'learned' 는 내가 가르친 가맹점 규칙,
  -- 'manual' 은 이 한 건만 손으로. 가맹점 규칙을 새로 가르칠 때 'manual' 은
  -- 덮어쓰지 않는다 - 한 건만 다르게 둔 결정을 일괄 변경이 지우면 안 된다.
  category_source text not null default 'rule'
                  check (category_source in ('rule', 'learned', 'manual')),
  approved_at   timestamptz not null,
  memo          text not null default '',
  -- 합계에서 뺄 것: 회사 경비로 돌려받을 돈, 내 계좌 사이의 이동 같은 것.
  excluded      boolean not null default false,
  -- 전액 취소가 짝을 찾으면 원래 승인에 취소 행 id 를 적는다. 둘 다 합계에서
  -- 빠진다. 짝이 없는 취소(부분 취소)는 음수로 합계에 들어간다.
  cancelled_by  bigint references card_transactions(id) on delete set null,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create index if not exists card_transactions_approved_idx
  on card_transactions (approved_at desc);
create index if not exists card_transactions_merchant_idx
  on card_transactions (merchant);

-- 카드사 채널에서 왔는데 결제처럼 보이지만 읽지 못한 메시지. 조용히 버리면
-- 그 결제는 가계부에서 영영 사라진다. 사람이 보고 적거나 버릴 때까지 남는다.
create table if not exists spending_unparsed (
  message_id  text primary key,
  chat_name   text not null,
  body        text not null,
  sent_at     timestamptz not null,
  resolved    boolean not null default false,
  created_at  timestamptz not null default now()
);

-- 내가 가르친 가맹점 → 분류. 키는 공백을 지우고 대문자로 바꾼 가맹점 이름이다.
create table if not exists merchant_rules (
  merchant_key text primary key,
  category     text not null,
  updated_at   timestamptz not null default now()
);

-- 월 예산. category 가 '' 이면 전체 예산이다. 금액 0 은 행을 지우는 것으로 표현한다.
create table if not exists spending_budgets (
  category   text primary key,
  amount_krw bigint not null check (amount_krw > 0),
  updated_at timestamptz not null default now()
);
