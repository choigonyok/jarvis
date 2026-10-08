-- KIS 접근 토큰. 하루 유효하고 발급은 앱 키당 1분에 한 번뿐이라, 메모리에만 두면
-- assets 컨테이너를 다시 띄울 때마다 새로 받다가 EGW00133 으로 막힌다.
-- 앱 키 자체가 아니라 그 해시로 찾는다.
create table if not exists kis_tokens (
  key_hash  text primary key,
  token     text not null,
  expires   timestamptz not null
);
