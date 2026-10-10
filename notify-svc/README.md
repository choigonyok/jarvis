# notify-svc

모든 서비스의 알림이 모이는 곳. 서비스는 **무슨 일이 있었고 얼마나 중요한지**만
정해진 모양으로 보내고, 그게 지금 푸시가 될지, 방해 금지 시간이 끝난 뒤 갈지,
저녁 요약의 한 줄이 될지, 알림함에만 남을지는 여기서 사람의 설정에 맞춰 정한다.

## 이벤트 보내기

```
POST /events
Authorization: Bearer $JARVIS_API_TOKEN

{
  "source": "spending",            // 보낸 서비스
  "kind":   "spending.big",        // "분류.세부". 분류 단위로 끌 수 있다
  "tier":   "digest",              // now | digest | log
  "level":  "info",                // info | warn | alert (선택, 기본 info)
  "title":  "큰 결제 ₩150,000",
  "body":   "가구점 · 10/10 14:03", // 선택
  "url":    "/spending",           // 누르면 열 콘솔 경로 (선택, 기본 /)
  "key":    "spending:big:123"     // 중복 방지. 같은 키는 한 번만 (선택)
}
```

응답: 새 알림이면 `201`과 저장된 알림, 이미 받은 키면 `200 {"duplicate":true}`,
모양이 틀리면 `400 {"error": "..."}`.

보내는 쪽은 실패해도 하던 일을 멈추지 않는다(각 서비스의 `internal/notify`,
assets-svc 의 `src/notify.ts` 는 기다리지 않고 보낸다). 같은 일을 여러 번 말해도
`key` 가 같으면 한 번이므로, 상태를 주기적으로 보고 "지금 이렇다"를 계속 보내도 된다.

### 단계(tier)

| tier | 언제 | 처리 |
|---|---|---|
| `now` | 사람이 손대야 하고 늦으면 손해 | 바로 푸시. 방해 금지 시간이면 모아 두었다 끝나는 시각에 한 통으로 |
| `digest` | 오늘 알면 되는 것 | 저녁 요약 시각(기본 21:00)에 한 통으로 |
| `log` | 궁금할 때 보는 기록 | 알림함에만 |

### 분류(kind 의 앞부분)와 지금 보내는 곳

| 분류 | 보내는 서비스 | 예 |
|---|---|---|
| `approval` | agent | 결재 대기 카드 (now) |
| `job` | agent | 작업이 로그인·확인을 기다림 (now) |
| `status` | status-svc | 검사가 이상으로 바뀜 (now), 주의 (digest), 다시 정상 (log) |
| `calendar` | calendar-svc | 상대(손님 에이전트)가 공유 일정 추가 (now) |
| `spending` | spending-svc | 큰 결제·해외 결제·취소·환불·예산 80% (digest), 예산 100% (now), 하루 지출 (digest) |
| `assets` | assets-svc | 비중 이탈·입출금 감지·공제 임박·원금 기록 불일치·하루 자산 (digest) |

새 분류를 쓰면 콘솔 알림 설정(`web/src/components/shell/notifications.tsx` 의
`CATEGORIES`)에도 넣어야 끌 수 있다.

## 그 밖의 경로 (콘솔용, 게이트웨이 `/notify` 를 거친다)

- `GET /notifications?limit=50` 알림함, `POST /notifications/read` `{ids}` 또는 `{}`(모두)
- `GET /settings`, `PUT /settings` `{muted, quietStart, quietEnd, digestAt}`
- `GET /vapid` 브라우저가 구독할 공개키, `POST|DELETE /subscriptions`
- `POST /test` 모든 기기에 시험 푸시

## 설정

`VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY` 는 `.env` 의 `JARVIS_VAPID_*` 에서 온다(공개
저장소라 코드에 두지 않는다). 없으면 푸시 없이 알림함에만 쌓인다. 저장은 Postgres
`notifications`, `push_subscriptions`, `notify_settings`(마이그레이션 031).
