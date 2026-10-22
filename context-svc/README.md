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
- **좌표마다 판단하지 않는다.** 이벤트는 영역 진입·이탈(`location.enter`/`location.leave`, 알림함에만)과
  현재 상태가 바뀔 때(`context.state`)뿐이다.
- **이동 시간은 여기서 잰다.** 지도 API 의 예상값을 저장하지 않는다. 실제로 걸린 시간은 체류에서 나온다(한 영역을
  나선 시각 → 다음 영역에 든 시각). 그 길을 자주 다니면 mind 가 이동 시간 노트로 만든다.

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

필드 이름은 OwnTracks 문서(owntracks.org/booklet, 2026-10-15 확인)와 맞췄다: `configuration.monitoring` 은 -1 Quiet,
0 Manual, 1 Significant, 2 Move. `bs`(배터리 상태)는 0 모름, 1 빠짐, 2 충전 중, 3 가득. HTTP 문서의 응답 예시에는
`setConfiguration` 이 없어서 iOS 앱 소스(owntracks/ios)로 확인했다: HTTP 응답 본문의 배열을 원소마다 `oneMessage` 로
넘기고, `cmd` 는 자기 기기 토픽으로 처리되어 앱의 `cmd` 설정이 켜져 있으면 `setConfiguration` 을, `remoteConfiguration`
이 켜져 있으면 적용한다(`Settings fromDictionary` 가 `monitoring` 을 바꾸고 위치 관리자에 바로 반영). 여기서 보내는 명령과
설정은 앱의 JSON 스키마(`message.json`/`messages.json`)로 검증했다.

## 읽는 것 (Bearer API 토큰)

```
GET    /now                 지금: 머무는 영역(언제부터), 마지막 위치와 나이, 신호, 한 줄 요약
GET    /visits?from=&to=    체류 (RFC 3339 또는 YYYY-MM-DD)
GET    /travel?days=60      체류에서 잰 이동: trips(나선 곳·시각 → 든 곳·시각, 분)와 routes(길마다 횟수, 걸린 시간의
                            중앙값, 보통 나서는 시각). 곳의 이름은 영역의 동네(area), 없으면 영역 이름. 3시간 넘게
                            어느 영역에도 없던 사이는 이동으로 치지 않고, 안에 든 영역(건물 ⊂ 동네)도 이동이 아니다
GET    /track?date=YYYY-MM-DD&who=owner|guest
                            그날(서울 기준)의 위치: fixes(시각·좌표·정확도·속도·움직임 - 와이파이 같은 나머지는
                            빼고, 거친 위치도 그대로), 그 전의 마지막 믿을 만한 위치(before, 하루가 시작된 곳), 겹치는 체류,
                            영역, 원본을 두는 날수(keepDays)와 남은 가장 오래된 위치의 날(since). 웹의 위치 탭이 이것으로
                            하루를 그리고 되감는다. who=guest 는 상대 폰의 것(체류는 없음)
GET    /owntracks/setup/guest  상대 폰의 OwnTracks 설정 링크: 상대 계정만 담고 운영자의 것(신호 토큰·단축어)은 없다
GET    /regions             PUT /regions {name, kind(home|work|camp|shop|food|other), lat, lon, radiusM, area}
DELETE /regions/{name}
```

## 폰 설정 (iPhone)

jarvis 웹의 **상태 → 폰 연결**을 아이폰에서 열고 차례로 누른다. 웹은 로그인 뒤에서 `GET /owntracks/setup`(API 토큰)을
읽어, OwnTracks 설정 전체(HTTP 모드, 게이트웨이 URL, Basic 계정, Significant, `cmd`·`remoteConfiguration` 켬)를
`owntracks:///config?inline=…` 링크 하나로 넘긴다. 비밀값은 버튼을 누를 때만 읽고 화면에 그리지 않는다.

1. OwnTracks(App Store, 게시자 Christoph Krey) 설치, 위치 권한 "항상 허용".
2. OwnTracks 설정에서 외부 설정 허용(URL·파일)을 켠다 - 26.2.3(2026-05)부터 보안 때문에 기본으로 꺼져 있고, 가져온
   설정으로는 켤 수 없다(앱 소스의 CHANGELOG).
3. "OwnTracks에 jarvis 설정 넣기" → 앱이 바뀌는 내용을 보여 주면 확인.
4. 영역(Regions)은 앱에서 만들면 된다: 앱이 보내는 `waypoints` 메시지를 받아 `context_regions` 에 이름으로 넣는다(종류는
   이름으로 짐작: "집"·"우리 집"은 home, "회사"·"사무실"은 work, 캠핑·마트·식당…, "지수네 집"은 other. 여기서 정한 종류와
   동네는 지키고 위치·반경만 갱신). 서버에 `PUT /regions` 로 넣어도 된다.
4. 단축어 개인용 자동화(CarPlay 연결/해제, 집중 모드 켬/끔, 충전기 연결/해제): "즉시 실행", "실행 시 알림" 끔,
   동작은 "URL 내용 가져오기" 하나 - POST `https://jarvis-be.choigonyok.com/context/signals`, 헤더
   `Authorization: Bearer <CONTEXT_SIGNAL_TOKEN>`, JSON `{"source":"shortcut","kind":"device.carplay","state":"connected"}`.
   첫 실행 때 도메인 연결을 "항상 허용".

## 상대(손님) 폰

손님 계정의 아이폰도 OwnTracks 로 위치를 보낼 수 있다. 자격은 따로다(`OWNTRACKS_GUEST_USER`/`OWNTRACKS_GUEST_PASSWORD`,
맥미니 `.env`). 같은 `POST /owntracks` 로 오지만 자격으로 갈라, 원본은 `context_raw` 에 `source = owntracks-guest` 로만 남는다:
운영자의 체류·현재 상태·`context.state`·영역(상대 폰이 보내는 waypoints)은 움직이지 않는다 - 이것들은 운영자 한 사람의 것이고,
mind·notify 가 그렇게 읽는다. 상대는 콘솔 위치 탭의 '내 폰 연결'에서 `GET /owntracks/setup/guest` 의 링크로 설정을 넣는다.

상대 폰에도 모드를 응답으로 바꿔 준다(`guestWatch`). 운영자 폰은 사건으로 바꾸지만(집·회사를 나서면 Move, 영역에 들면
Significant), 상대 폰은 지금 있는 자리로 정한다: 영역 안(여기 등록된 영역이나 폰이 스스로 등록한 영역 - 보고의 `inregions`)이거나
150m 안에 15분 넘게 있었으면 Significant, 그 밖에서 움직이면 Move. 차 안(`automotive`)은 막혀 서 있어도 움직이는 중으로 본다.
Move 의 타이머 보고(`t: t`)가 멈춰 있는 동안에도 와서, 15분이 지나면 이 판단이 내려진다. 배터리 규칙은 운영자 폰과 같다.

## 현재 상태 한 장

`/now` 의 `state` 는 체류와 신호에서 만든 파생값이다: 운전 중(CarPlay 연결 4시간 안, 또는 운전 집중 모드), 회의 중
(맥 앞의 앱이 회의 앱, 15분 안), 집·회사(머무는 영역의 종류), 집중 모드. 오래된 신호는 믿지 않는다. agent 는 매
턴 이 한 줄을 지금 상황 옆에 둔다.

**건강 기저선**: 단축어가 매일 아침 `health.hrv`·`health.hr_rest`·`health.sleep`·`health.steps` 를 숫자로 보내면(`건강 샘플 찾기`
→ URL 내용 가져오기), `/now` 의 `health` 가 마지막 값을 **내 평소**(그 전 28일, 하루 한 값의 중앙값, 7일 이상 쌓여야)와 견주고,
15% 넘게 벗어나면 한 줄 요약에 "심박 변이(HRV) 38 (평소 45보다 16% 낮음)"처럼 넣는다. 원본은 다른 신호처럼 30일만 둔다.

이 한 장이 바뀌면(보고가 올 때마다, 그리고 신호가 낡아 끝나는 것까지 1분마다 확인) `context.state` 이벤트를 낸다:
`{state, changed: ["운전 시작", "집 끝"], summary}`. 마지막으로 알린 상태는 `context_state` 의 `derived` 키에 둔다.
notify-svc 가 이것을 듣고 **운전·회의 중에는 즉시 알림을 붙잡았다가** 끝나면 "운전·회의 중 알림 N건"으로 한 번에
보낸다(방해 금지 시간과 같은 방식, 끝을 못 들어도 4시간 뒤 풀림). agent 의 Source 인터페이스(`module.Source`)는
실행 경로가 없어서(레지스트리가 모으기만 한다) 쓰지 않았다: 에이전트는 이벤트를 events-svc 구독(`/consume`)으로 받고,
상태는 매 턴 읽는다.

## 맥 수집기

`cmd/mac-collector` 는 맥에서 하나만 도는 읽는 프로세스다(kakaotalk-client 처럼). `scripts/mac-collector install`
로 빌드해 LaunchAgent(`com.jarvis.mac-collector`)로 띄우고, 신호 토큰은 설치할 때 맥미니 `.env` 에서 읽어
`~/.config/jarvis/signal-token`(0600)에 둔다. 보내는 것은 **종류뿐**이다:

- `mac.app`: 앞에 있는 앱의 종류(meeting · code · chat · browser · other). 앱 이름·창 제목은 보내지 않는다.
- `mac.clipboard`: 방금 복사한 것의 종류(tracking 송장번호 · address 주소 · date 날짜). 내용은 보내지 않고,
  비밀번호 관리자가 앞에 있을 때는 클립보드를 보지도 않는다.
- `mac.download`, `mac.screenshot`: 새 파일의 확장자. macOS 가 폴더 접근을 물어서 기본으로 끈다(`-files`).

카메라·마이크 사용 여부는 권한 없이 믿을 만하게 읽을 길이 없어, 회의 중은 앞에 있는 앱으로만 판단한다.
지우려면 `scripts/mac-collector uninstall`.

## 검증한 것

- `internal/place`: 거리, 정확도를 감안한 영역 포함, 모드 전환 규칙(배터리 포함).
- `internal/api` (Postgres, `CONTEXT_TEST_DATABASE_URL`): Basic 인증 거부, 위치만으로 체류 열기, 이탈 이벤트로
  Move 명령, 진입 이벤트가 빠져도 다음 위치로 체류를 다시 열고 Significant 명령, 신호 토큰으로 쓰고 읽지 못함,
  잘못된 kind 거부, 지금·체류 조회, 오래된 원본 지우기.
- `cmd/mac-collector`: 앱 종류, 클립보드 패턴(키처럼 생긴 문자열은 아무 종류도 아님), 비밀번호 관리자.
- 건강 기저선: 엿새 치로는 말하지 않고, 같은 날 여러 값이면 마지막 값, 중앙값 대비 15%. API 흐름에서 안정 시 심박 아흐레 58 뒤 70 →
  "평소 58보다 21% 높음", 원래 `health.hr_rest=70` 꼴로는 요약에 나오지 않음.
- 현재 상태 파생: 오래된 회의 신호는 무시, CarPlay 연결이면 운전 중. 바뀐 것의 말(`운전 시작,집 끝`).
- 이동(`place.Trips`·`Routes`): 같은 동네 안의 영역 이동, 다른 체류 안에 든 짧은 체류, 3시간 넘는 공백은 이동이
  아니고, 걸린 시간은 중앙값. API 흐름에서 집 → 회사 30분, 회사 → 집 67분이 `/travel` 로 나오고, 신호가 오면
  `derived` 상태가 운전 중으로 바뀐다.
- 운영에서 게이트웨이를 거쳐: 토큰 없는 `/context/now` 는 401, 자격 없는 `/context/owntracks` 도 401.
- **실제 OwnTracks 앱으로**(2026-10-15, owntracks/ios 소스를 Xcode 26.2 로 빌드해 iOS 26.2 시뮬레이터에서, 로컬 context-svc 와 임시
  Postgres 상대로 - 운영 데이터는 건드리지 않음): UI 테스트가 `/owntracks/setup` 의 링크를 열고 "Process Configuration → Continue"를
  누르면 앱 설정에 `mode 3`·URL·계정·`cmd 1`·`remoteConfiguration 1`·`monitoring 1` 이 들어간다. 위치를 집 → 강남으로 옮기며 "Send
  location now" 를 누르면 집 체류가 열렸다 닫히고, 서버가 응답에 `setConfiguration monitoring 2` 를 실어("폰의 위치 모드를 바꿉니다
  from=1 to=2") 앱 화면의 모드가 Significant → Move 로 바뀌며, 그다음 보고부터 `m=2` 로 온다. 시험 빌드에서 바꾼 것은 "외부 설정
  허용" 기본값(폰에서는 사람이 켜는 스위치), 로컬 http 를 위한 ATS 완화, 이 Xcode 에 맞춘 프로젝트 형식뿐이다.
- **실제 아이폰으로**(2026-10-15, OwnTracks 27.0.1, iOS 27.0): 웹의 폰 연결로 설정을 넣자 게이트웨이로 보고가 들어왔다(tid `me`,
  Basic 인증 통과). `PUT /owntracks/cmd {"action":"status"}` 에 폰이 상태 메시지로 답했고, `PUT /owntracks/mode {"mode":2}` 를 걸자
  다음 보고의 응답으로 setConfiguration 이 나가 그 뒤 보고가 `m=2`(Move)로 왔다. 처음 한 번은 원격 설정 스위치가 꺼져 있어 적용되지
  않았는데, 그때 서버가 보낸 것을 바뀐 것으로 쳐서 다시 보내지 않던 것을 고쳤다: 이제 보고의 `m` 이 기준이다.
- 폰 설정 링크(Postgres 흐름 테스트): 토큰 없이 401, inline 을 풀면 HTTP 모드·URL·계정·Significant·cmd·remoteConfiguration.
  waypoints 로 영역이 들어오고 여기서 정한 종류·동네는 남는다. 수집기의 배달앱 알림 줄 읽기(다시 넣기·다른 앱은 무시).
