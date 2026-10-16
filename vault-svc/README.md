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
```

## 토큰 둘

| 토큰 | 누가 | 열리는 것 |
|---|---|---|
| `API_TOKEN` (서비스 토큰) | 에이전트 | `GET /logins`(비밀 없음), `PUT /logins/{domain}`, `DELETE /logins/{domain}`, `GET /uses` |
| `VAULT_RELEASE_TOKEN` | 브라우저 컨테이너 | `POST /release` 하나 |

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
