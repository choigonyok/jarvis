-- 주문자·배송지(받는 분·연락처·이메일·우편번호·주소·상세주소) 보관함. 운영자가 대화의 카드로 한 번 넣으면
-- 작업과 대화가 주문서를 채울 때 그대로 꺼내 쓴다 - 주문할 때마다 카드로 다시 묻다가 연락처·건물번호가
-- 틀리게 들어간 일(2026-10-29~30 장범준 LP, 주문서 네 번이 틀린 건물번호)이 있어서다. 결제 비밀번호와
-- 달리 모델이 읽는다(주문서에 치려면 알아야 한다). 그래도 개인정보라 봉인해 둔다(같은 키, 봉인의 AAD 는
-- 'profile:' + 이름). 이 표를 읽고 쓰는 역할은 svc_vault 하나뿐이다.
create table if not exists vault_profiles (
  -- 배송지의 이름: '기본' 하나로 시작한다.
  name         text primary key,
  nonce        bytea not null,
  sealed       bytea not null,
  -- 어느 키로 봉인했는지(키의 해시 앞부분). 키가 바뀌면 다시 저장해야 한다.
  key_id       text not null,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

grant select, insert, update, delete on vault_profiles to svc_vault;
