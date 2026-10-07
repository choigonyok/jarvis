# market-svc

중고나라 판매 글의 기록과, 중고나라에 반영할 변경의 큐. 포트 8097.

중고나라와 직접 말하지 않는다. 흐름은 이렇다.

1. 대화에 사진을 첨부하고 팔아 달라고 하면, 에이전트가 사진을 보고 `search_prices`로 시세를 확인한 뒤 `post_listing` 초안 카드를 올린다.
2. 운영자가 카드에서 제목·가격·상태·택배비·설명을 고쳐 승인하면 에이전트가 `POST /listings`로 넣는다(등록 작업이 함께 생긴다).
3. 에이전트의 백그라운드 작업자(`agent/internal/module/market/worker.go`)가 대화가 없을 때 `GET /tasks/next`로 일을 받아 브라우저로 처리하고 `POST /tasks/{id}/report`로 결과를 적는다.
4. 한 시간마다(`MARKET_SYNC_EVERY`) 판매중·예약중 글을 열어 상태·가격·조회·찜·채팅 수를 `POST /sync`로 적는다.
5. 판매가 끝나고 30일(`MARKET_PURGE_AFTER`)이 지난 글의 사진은 작업자가 지운다.

중고나라 탭은 `GET /listings`로 현황을 보고, `POST /listings/{id}/tasks`로 가격 변경·상태 변경·끌어올리기·삭제를 큐에 넣는다.

## 경로

| 경로 | 호출자 | 하는 일 |
| --- | --- | --- |
| `GET /listings` | 탭 | 글 목록, 처리 중·실패한 작업, 동기화 상태 |
| `POST /listings/{id}/tasks` | 탭 | `{kind: price\|status\|bump\|delete\|post, priceKrw?, toStatus?}` |
| `DELETE /tasks/{id}` | 탭 | 아직 시도하지 않은 작업 취소(등록 취소는 초안도 지움) |
| `POST /sync/request` | 탭 | 다음 차례에 바로 동기화 |
| `POST /login/resolved` | 탭 | 다시 로그인했으니 멈춘 작업을 이어서 |
| `POST /listings` | 에이전트 | 승인된 초안 |
| `GET /tasks/next`, `POST /tasks/{id}/report` | 작업자 | 일 받기, 결과(`done`·`failed`·`login_required`) |
| `GET /sync/due`, `POST /sync` | 작업자 | 동기화 대상, 결과 |
| `GET /purge/due`, `POST /listings/{id}/purged` | 작업자 | 사진 정리 |

`login_required`가 한 번 오면 모든 작업이 멈추고, 탭에 로그인 배너가 뜬다. 실패는 15분 간격으로 세 번까지 다시 시도한다.
