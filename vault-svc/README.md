# vault-svc — 로그인 정보 보관함

운영자의 사이트 로그인(아이디·비밀번호)을 봉인해서 두고, 브라우저가 **그 사이트의 로그인 화면에
있을 때만** 꺼내 준다. 모델(Claude)은 값을 한 번도 보지 않는다.

```
대화: "쿠팡 로그인 정보 저장해줘"
  → 자비스가 save_login → 승인 카드(아이디 칸 + 비밀번호 칸, 둘 다 private)
  → 운영자가 칸에 넣고 승인 → 에이전트가 그 두 값만 따로 떼어(SecretSink) PUT /logins/coupang.com
    (카드 기록·대화·모델에는 "저장됨(아이디 ab***)"만 남는다)

로그인 화면:
  → 모델이 browser_fill_login (인자 없음)
  → 브라우저 브리지가 지금 탭 주소를 직접 읽어 POST /release {host}
  → 그 주소의 도메인(coupang.com)에 저장된 것만 받아 칸에 바로 넣고 버린다
  → 모델은 "넣었습니다"만 받는다

결제 비밀번호(네이버페이·쿠페이 숫자 6자리)도 같은 길로, 로그인과 따로 둔다(vault_pins, 073):
  → 자비스가 save_payment_pin {pay: 네이버페이} → 승인 카드(비밀 칸 하나) → PUT /pins/pay.naver.com
  → 결제 비밀번호 키패드 앞에서 browser_enter_payment_pin 이 키패드 프레임의 호스트를 읽어
    POST /release-pin {site: pay.naver.com, host}
  → host 가 그 결제 호스트(와 그 아래)일 때만 내준다 - 같은 naver.com 이어도 블로그·카페는 아니다

주문자·배송지(받는 분·연락처·이메일·우편번호·주소·상세주소)도 카드로 받아 봉인해 둔다(vault_profiles, 074):
  → 자비스가 save_shipping_address(대화) 또는 request_shipping_address(작업) → 승인 카드(칸 여섯, 값은 카드 기록에 안 남음)
  → PUT /profiles/기본 - 주소는 도로명과 건물번호까지가 아니면 거절한다(주문서의 주소 검색이 그것으로 찾는다)
  → 주문서를 채울 때 `look` 의 shipping_address 와 작업의 shipping_address 가 GET /profiles 로 통째로 읽는다
    (비밀번호와 달리 모델이 읽는다 - 주문서에 치려면 알아야 한다. 그래도 개인정보라 봉인)
```

결제 비밀번호를 로그인과 따로 두는 것은, 로그인은 등록 도메인마다 하나(naver.com)라서 네이버 로그인과
네이버페이 결제 비밀번호가 한 칸을 두고 다투기 때문이다. 둘 곳은 `PinSites`(pay.naver.com, coupang.com)뿐이고,
봉인의 AAD 는 `pin:` + 결제 호스트라 로그인 봉인과 섞이지 않는다.

## 토큰 둘

| 토큰 | 누가 | 열리는 것 |
|---|---|---|
| `API_TOKEN` (서비스 토큰) | 에이전트 | `GET /logins`(비밀 없음), `PUT /logins/{domain}`, `DELETE /logins/{domain}`, `GET /uses`, `GET /pins`(숫자 없음), `PUT /pins/{site}`, `DELETE /pins/{site}`, `GET /profiles`(배송지 통째로), `PUT /profiles/{name}`, `DELETE /profiles/{name}` |
| `VAULT_RELEASE_TOKEN` | 브라우저 컨테이너 | `POST /release`, `POST /release-pin` 둘 |

서비스 토큰으로는 비밀번호를 꺼낼 수 없고, 브라우저 토큰으로는 저장·목록·지우기를 못 한다.
둘이 같으면 서비스가 뜨지 않는다.

## 봉인

AES-256-GCM, 쓸 때마다 새 nonce, 연관 데이터는 `jarvis-vault/v1/<domain>` - 봉인된 행을 다른 도메인으로
옮기면 열리지 않는다. 키는 `VAULT_KEY`(맥미니 .env 의 `JARVIS_VAULT_KEY`, `openssl rand -base64 32`)에만 있고
DB·백업에는 없다(백업에는 봉인된 값뿐). **키를 잃으면 저장된 정보는 다시 넣어야 한다** - 따로 보관해 둘 것.
행마다 키의 해시 앞부분(`key_id`)을 남겨, 키가 바뀌면 "다른 키로 저장됨"이라고 알린다.

## 도메인

로그인은 등록 도메인에 묶인다(`gocommon/site`): `login.coupang.com` → `coupang.com`, `www.11st.co.kr` → `11st.co.kr`.
그 도메인과 하위 주소의 화면에서만 꺼내 준다(`coupang.com.evil.com` 은 아니다).

## 기록

`vault_uses` 에 꺼낼 때마다 남는다: 언제, 어느 화면(host), 무엇 때문에(대화/작업), 결과(released·none·refused·failed).
상태 탭 → 로그인 정보에서 보인다. 자동 재로그인(에이전트의 로그인 확인이 로그아웃을 찾으면 저장된 정보로 한 번)도
같은 길로 꺼낸다.

## 실행

마이그레이션 `059_vault.sql`, 역할 `svc_vault`(비밀번호는 `scripts/db-roles`), 포트 8105(공개 포트 없음,
`data`·`browser` 네트워크). 테스트: `go test ./...`, DB 는 `scripts/dbtest vault-svc`.
