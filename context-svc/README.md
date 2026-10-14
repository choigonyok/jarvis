# context-svc

지금 어디에 있고 어떤 상태인지. 폰의 **OwnTracks** 가 위치와 영역 진입·이탈을, **iOS 단축어**와 **맥 수집기**가
기기 상태(CarPlay 연결, 집중 모드, 충전, 맥 앞의 앱 종류)를 보낸다. 이것을 영역 **체류(visits)** 와 **현재
상태 한 장**으로 만든다. 무엇을 할지는 정하지 않는다: mind 가 체류를 일어난 일로 읽고(지금 상황, 지각 예고,
일정 중 링크), agent 가 현재 상태를 대화의 사실로 읽는다(`get_context` 도구).

## 경계를 이렇게 그은 이유

- **위치와 기기 신호를 서비스 하나로.** 둘 다 "지금 상태"를 말하고, 받는 방식(폰이 밀어 넣음), 보관 원칙(원본은
  짧게, 파생은 길게), 읽는 쪽(mind·agent)이 같다. 나누면 같은 "현재 상태 한 장"을 두 곳에서 합쳐야 한다.
- **폰은 스택의 토큰을 갖지 않는다.** 게이트웨이는 `/context/owntracks` 와 `/context/signals`(POST)만 토큰
  없이 들여보내고, 여기서 각자의 자격으로 검사한다: OwnTracks 는 HTTP Basic(`OWNTRACKS_USER`/`OWNTRACKS_PASSWORD`),
  단축어·수집기는 신호 토큰(`CONTEXT_SIGNAL_TOKEN`). 신호 토큰으로는 아무것도 읽지 못한다.
- **원본 좌표는 30일만**(`CONTEXT_RAW_DAYS`). 원본은 공용 `raw_events` 가 아니라 `context_raw` 에 둔다:
  `raw_events` 는 추가 전용 트리거가 있어 지울 수 없고, 위치 원본은 지워야 한다. 체류·현재 상태는 오래 둔다.
- **좌표마다 판단하지 않는다.** 이벤트는 영역 진입·이탈(`location.enter`/`location.leave`, 알림함에만)뿐이다.

## 받는 것

```
POST /owntracks   OwnTracks HTTP 모드 (Basic). {"_type":"location", lat, lon, acc, tst, batt, bs, inregions}
                  또는 {"_type":"transition", event: enter|leave, desc: 영역 이름, tst}
                  응답: [] 또는 모드를 바꾸는 명령
                  [{"_type":"cmd","action":"setConfiguration","configuration":{"_type":"configuration","monitoring":2}}]
POST /signals     {source, kind, state, at?} (Bearer 신호 토큰). kind 는 device.carplay, device.focus,
                  device.charging, mac.app 처럼 소문자 점 표기, state 는 짧은 값
```

체류는 **스스로 바로잡는다**: 진입·이탈 이벤트를 놓쳐도 다음 위치(정확도 300m 이내)가 어느 영역 안인지 말하므로
(폰의 `inregions` 와 여기 저장된 원) 열린 체류를 닫고 새로 연다. 신호도 "바뀜"이 아니라 "상태"를 보내므로 하나를
놓쳐도 다음 것이 고친다.

모드 전환(응답 명령): 집·회사를 떠나면 Move(자세히), 어딘가 도착하면 Significant(아낌), 배터리 20% 미만이면
Move 를 하지 않고, 8% 미만이면 Quiet. 서버가 먼저 폰을 깨울 수 없어서, 바꾸는 계기는 늘 폰이 보낸 보고다.
OwnTracks 에서 원격 설정(Remote configuration)을 켜야 적용된다.

## 읽는 것 (Bearer API 토큰)

```
GET    /now                 지금: 머무는 영역(언제부터), 마지막 위치와 나이, 신호, 한 줄 요약
GET    /visits?from=&to=    체류 (RFC 3339 또는 YYYY-MM-DD)
GET    /regions             PUT /regions {name, kind(home|work|camp|shop|food|other), lat, lon, radiusM, area}
DELETE /regions/{name}
```

## 폰 설정 (iPhone)

1. OwnTracks(App Store, 게시자 Christoph Krey) 설치, 위치 권한 "항상 허용".
2. 설정 → 연결: 모드 HTTP, URL `https://jarvis-be.choigonyok.com/context/owntracks`, 인증 사용자
   `OWNTRACKS_USER`, 비밀번호 `OWNTRACKS_PASSWORD`(맥미니 `.env`). 원격 설정 허용.
3. 모니터링 Significant. 영역(Regions)에 집·회사를 추가하거나, 서버에 `PUT /regions` 로 넣는다(둘 다 쓰인다).
4. 단축어 개인용 자동화(CarPlay 연결/해제, 집중 모드 켬/끔, 충전기 연결/해제): "즉시 실행", "실행 시 알림" 끔,
   동작은 "URL 내용 가져오기" 하나 - POST `https://jarvis-be.choigonyok.com/context/signals`, 헤더
   `Authorization: Bearer <CONTEXT_SIGNAL_TOKEN>`, JSON `{"source":"shortcut","kind":"device.carplay","state":"connected"}`.
   첫 실행 때 도메인 연결을 "항상 허용".

## 검증한 것

- `internal/place`: 거리, 정확도를 감안한 영역 포함, 모드 전환 규칙(배터리 포함).
- `internal/api` (Postgres, `CONTEXT_TEST_DATABASE_URL`): Basic 인증 거부, 위치만으로 체류 열기, 이탈 이벤트로
  Move 명령, 진입 이벤트가 빠져도 다음 위치로 체류를 다시 열고 Significant 명령, 신호 토큰으로 쓰고 읽지 못함,
  잘못된 kind 거부, 지금·체류 조회, 오래된 원본 지우기.
- 운영에서 게이트웨이를 거쳐: 토큰 없는 `/context/now` 는 401, 자격 없는 `/context/owntracks` 도 401.
