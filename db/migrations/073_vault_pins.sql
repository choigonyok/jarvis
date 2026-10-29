-- 결제 비밀번호(쿠팡 쿠페이·네이버페이, 숫자 6자리) 보관함. 운영자가 대화의 카드(비밀 칸)로 넣은 값을
-- vault_logins 처럼 봉인해 두고(같은 키, 봉인의 AAD 는 'pin:' + 결제 호스트), 브라우저의 결제 비밀번호
-- 도구(browser_enter_payment_pin)가 그 결제 호스트의 키패드 앞에 있을 때만 꺼내 준다. 모델은 값을 보지
-- 못한다. 로그인과 따로 두는 것은, 로그인은 등록 도메인마다 하나(naver.com)라서 네이버 로그인과 네이버페이
-- 결제 비밀번호가 한 칸을 두고 다투기 때문이다. 이 표를 읽고 쓰는 역할은 svc_vault 하나뿐이다.
create table if not exists vault_pins (
  -- 키패드가 뜨는 결제 호스트(그 아래 주소 포함): pay.naver.com, coupang.com
  site         text primary key,
  name         text not null default '',
  nonce        bytea not null,
  sealed       bytea not null,
  -- 어느 키로 봉인했는지(키의 해시 앞부분). 키가 바뀌면 다시 저장해야 한다.
  key_id       text not null,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  last_used_at timestamptz,
  uses         integer not null default 0
);

grant select, insert, update, delete on vault_pins to svc_vault;
