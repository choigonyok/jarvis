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
                  - 하루 첫 위치 보고에는 매일 되돌리는 설정과 영역 목록 요청도(아래 '매일 되돌리는 것')
POST /signals     {source, kind, state, at?} (Bearer 신호 토큰). kind 는 device.carplay, device.focus,
                  device.charging, mac.app 처럼 소문자 점 표기, state 는 짧은 값
```

체류는 **스스로 바로잡는다**: 진입·이탈 이벤트를 놓쳐도 다음 위치(정확도 300m 이내)가 어느 영역 안인지 말하므로
(폰의 `inregions` 와 여기 저장된 원) 열린 체류를 닫고 새로 연다. 신호도 "바뀜"이 아니라 "상태"를 보내므로 하나를
놓쳐도 다음 것이 고친다.

모드 전환(응답 명령): 폰이 지금 있는 자리로 정한다. 영역 안(여기 등록된 영역이나 폰이 스스로 등록한 영역 - 보고의 `inregions`)이거나 150m 안에 15분 넘게
있었으면 Significant(아낌), 그 밖에서 움직이면 Move(자세히). 차 안(`automotive`)은 막혀 서 있어도 움직이는 중이다. 영역
진입·이탈 보고는 들어가면 Significant, 나와서 어느 영역에도 없으면 바로 Move - 두 폰 모두. '15분 넘게 한자리'는 마지막으로
영역을 나선 때(이탈 보고나 체류 종료)와 마지막으로 원 안에 있던 위치 뒤부터 센다: 집에 있던 몇 시간이 집을 막 나선 위치를
'한자리'로 만들어 다시 Significant 로 되돌리지 않게. 늦게 온 위치나 거친 위치는 판단하지 않는다.
Move 의 타이머 보고(`t: t`)가 멈춰 있는 동안에도 와서, 15분이 지나면 판단이 내려진다. 배터리 20% 미만이고 충전 중이
아니면 Move 대신 Significant(OwnTracks 의 다운그레이드와 같은 기준) - Quiet 으로는 보내지 않는다: 조용한 폰은 보고가 없어
서버가 다시 깨울 수 없다. 전환 메시지에는 배터리가 없어 마지막 위치의 배터리를 쓴다. 손으로 건 모드(`PUT
/owntracks/mode`)는 한 시간 동안 이 규칙보다 앞선다. 서버가 먼저 폰을 깨울 수 없어서, 바꾸는 계기는 늘 폰이 보낸 보고다.
OwnTracks 에서 원격 설정(Remote configuration)을 켜야 적용된다. 판단과 그 이유(`region|still|car|moving|left|held`,
배터리, `downgraded`)는 폰마다 `owntracks.watch(.guest)` 에 두고 `/track` 이 `watch` 로 내준다 - 위치 탭이 모드와 배터리
때문에 Move 를 쉬는 것을 보여 준다.

매일 되돌리는 것(`phones.go`): 폰마다 서울 기준 하루의 첫 위치 보고에 답하면서 OwnTracks 설정을 되돌린다 -
`extendedData` 켬(모드 `m` 과 움직임이 이것과 함께 온다), `downgrade` 20, `ignoreInaccurateLocations` 100,
`locatorInterval` 60, `locatorDisplacement` 50, `allowRemoteLocation` 켬(`setConfiguration` 은 적은 키만 바꾼다, iOS 소스
`Settings fromDictionary`).
같은 답에 `{"_type":"cmd","action":"waypoints"}` 로 영역 목록 전체를 받고, 반경이 50m 가 아닌 영역은 그 답에
`setWaypoints` 로 50m 로 돌려놓는다 - 앱은 영역을 `rid` 로 찾아 바꾸므로 폰이 보낸 `rid`·`tst` 를 그대로 쓴다(없으면
새로 추가돼 둘이 된다). 여기 둔 영역(`context_regions`, 상대는 `guest.regions`)도 그날 50m 가 된다. 이름만 붙인 점(반경
0)은 받는 즉시 50m 영역으로 바꿔 보낸다(앱은 반경 0 을 감시하지 않는다). 손으로 바꾼 반경은 다음 날까지 둔다. 폰의 목록과
id 는 운영자 `owntracks.regions`, 상대 `guest.regions` 에 둔다.

따라다니는 영역(`+follow`): 목록에 이름이 `+` 로 시작하는 영역이 없으면 같은 답의 `setWaypoints` 에
`+follow`(rid `follow`, 50m, 그 폰의 마지막 위치)를 넣는다. iOS 앱은 `+` 영역을 위치를 보낼 때마다 그 자리로 옮기고(반경은
속도×30초, 속도를 모르면 정확도, 최소 50m) 그 원을 벗어나면 Move 가 아니어도 바로 위치를 보낸다(`t: C`, 소스
`publishLocation`·`regionEvent`). 등록 영역이 아닌 곳에서 한자리(still)로 Significant 가 된 뒤 떠나도 100~300m 안에 위치가
와서 판단이 다시 돈다 - 없으면 iOS 의 큰 위치 변화(500m 넘게, 몇 분 뒤)만 남아 출발 직후가 '기록 없이 이동'이 된다
(10/25 캠핑장: 첫 위치가 576m 떨어진 곳에서 7분 뒤). 장소가 아니다: 앱이 `inregions` 와 전환 메시지에서 빼고, 여기서도
영역 목록·50m 되돌리기·체류에 넣지 않으며(`+` 로 시작하는 전환은 무시), 위치 등록 이름도 `+` 로 시작할 수 없다. 앱은 rid 가
같으면 자리·반경이 달라도 같은 것으로 보므로 다시 보내도 겹치지 않는다. 하루 점검이 하는 일이 바뀌면(`dailyRev`) 그날 이미
점검한 폰도 다음 위치 보고에 한 번 더 한다 - 배포한 날 바로 받게.

처음 Move 로 바꾸는 답에는 `{"_type":"cmd","action":"reportLocation"}` 도 싣는다(`place.Tell`, 두 폰).
앱은 명령을 차례로 처리해 Move 로 바꾼 다음 그때 가진 가장 새 위치(`LocationManager.location`)를 `t: r` 로 보낸다 - 실제
앱(시뮬레이터)에서 Move 로 바뀐 1초 뒤 새 시각의 위치가 왔다(그 답을 부른 보고는 1분 전 위치였다). 그래서 Move 의 길이 Move
가 스스로 재는 첫 위치(50m 또는 1분 뒤)가 아니라 바뀐 순간부터 시작한다. 폰이 아직 받지 않아 Move 를 다시 보낼 때는 묻지
않는다(마지막으로 보낸 모드가 이미 Move). 앱의 '원격 위치 허용'(`allowRemoteLocation`, 기본 켬)이 꺼져 있으면 답하지
않으므로 매일 되돌리는 설정에 넣었다.

위치 등록(`POST /owntracks/regions`, 상대는 `/owntracks/regions/guest`, `{name, lat, lon}`): 콘솔 위치 탭에서 지금 있는
곳을 그 사람 폰의 50m 영역으로 만든다. 여기에는 바로 넣고(있는 곳이면 도착 알림 없이 체류로), 폰에는 다음 보고의 답에
`setWaypoints` 와 목록 요청을 실어 보낸다 - 폰의 목록에 보일 때까지 10분마다, 일주일까지(`owntracks.push(.guest)`). 같은
이름이 있으면 그 영역을 옮긴다(같은 `rid`). 폰의 목록을 아직 받은 적이 없으면(`owntracks.listed(.guest)` 없음) 목록부터
받아 같은 이름이 있는지 보고 보낸다. 목록을 알면 답에 `owntracks:///config?inline=` 링크도 실어, 폰에서 눌러 바로 넣을 수
있다(앱이 바뀌는 내용을 보여 주고 묻는다). `/track` 의 `pending` 이 아직 폰에 가는 중인 이름이다.

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
                            하루를 그리고 되감는다. who=guest 는 상대 폰의 것: 체류는 없고, regions 는 상대 폰의
                            영역, otherRegions 는 운영자의 영역(상대가 영역을 두지 않은 곳의 이름)
GET    /owntracks/setup/guest  상대 폰의 OwnTracks 설정 링크: 상대 계정만 담고 운영자의 것(신호 토큰·단축어)은 없다
POST   /owntracks/regions   {name, lat, lon} 위치 등록 - 운영자 폰의 50m 영역(위 '위치 등록'). 상대 것은 /owntracks/regions/guest
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
운영자의 체류·현재 상태·`context.state`·영역은 움직이지 않는다 - 이것들은 운영자 한 사람의 것이고,
mind·notify 가 그렇게 읽는다. 상대는 콘솔 위치 탭의 '내 폰 연결'에서 `GET /owntracks/setup/guest` 의 링크로 설정을 넣는다.

상대 폰에서 만든 영역은 상대 것으로 따로 둔다(`context_state` 의 `guest.regions`, 운영자의 `context_regions` 와 섞지 않음).
앱은 영역 하나를 저장할 때마다 `waypoint`(앱의 id `rid` 와 함께)를 보내므로 이름을 바꿔도 같은 영역으로 바뀌고, 이름을
붙이기 전의 점(앱의 "+" 가 처음 만드는 Here-…)은 영역이 아니다. 지운 영역은 앱이 알리지 않아서, 매일 첫 보고 때 받는 영역
목록 전체로 맞춘다(위 '매일 되돌리는 것').
상대가 자기 영역에 들고 나면(전환 메시지, 또는 위치가 든 원·앱의 `inregions`) `location.guest.enter|leave` 이벤트를 낸다 -
알림은 운영자의 위치 알림과 같은 모양(`log`, 알림함에만)이고 받는 사람은 손님(`audience: guest`)이다. 운영자의
`location.enter|leave` 와 이름이 달라 mind 의 생활 판단에는 들어가지 않는다. 지금 든 영역은 `guest.inside` 에 두고, 늦게 온
보고는 무시하며, 첫 보고와 영역을 새로 만들거나 이름을 바꾸는 순간은 도착·출발로 치지 않는다.

상대 폰의 모드도 운영자 폰과 같은 규칙으로 응답에서 바꾼다(`guestWatch`, 위의 모드 전환, 상대 영역 안도 자리 잡은 것.
자기 영역을 나서는 전환 메시지에도 바로 Move) - 손으로 거는 모드만 없다.

## 출발 (실시간 버스·지하철)

콘솔 출발 탭의 것(`internal/api/transit.go`, `internal/transit`). 사람마다(운영자 `transit.routes`, 손님
`transit.routes.guest`, `context_state`) 자주 타는 경로 - 탈 정류장·역, 노선(지하철은 노선과 방향), 걸어가는 분,
일찍 도착할 분, 요일·시간대·알림 - 를 두고, 지금 오는 차 중 나가서 탈 수 있는 첫 차와 나갈 때를 센다(`transit.Make`:
나갈 때(`leave`)는 걸음+여유만큼 앞). 여유는 바람일 뿐 한계가 아니어서, 걸어서 닿는 동안은 그 차가 탈 차이고(`last`), 걸음보다
2분까지 늦은 것은 '뛰면 탈 수 있어요'다. 버스 피드는 다음 두 대만 알려 주므로, 여유까지 빼면 버스가 눈앞에 오는데 탈 차가 없다고
나왔다. 걸어서 못 타는 차는 `missed` 에 실어 화면이 보여 준다. 말은 남은 시간으로: 20분 넘게 '여유 있어요', 20분 안
'슬슬 짐 싸야 해요', 10분 안 '곧 나가야 해요', 3분 안 '지금 나가요!'.

차마다 `away`(몇 번째 전), `seats`(좌석 버스의 빈자리), `crowd`(여유·보통·혼잡·매우혼잡·만차)를 싣는다. 한 노선이 한 정류장을
양방향으로 지나면 피드에 두 번 나오고 노선 id 는 같다 - 경로의 `line.order`(그 정류장의 노선 순번 `staOrder`, 예전 경로는 행선지)로
가는 쪽만 탄다(`oneWay`).

```
GET /transit                 운영자의 경로와 지금의 계획(plans), 키가 있는 피드(feeds)
GET /transit/guest           손님의 것
PUT /transit/routes          운영자의 경로 전체 {routes}, PUT /transit/guest/routes 는 손님의 것
GET /transit/stops?near=위도,경도|me&q=이름   정류장·역 찾기(me: 그 사람 폰의 마지막 위치), /transit/guest/stops
GET /transit/lines?kind=bus|subway&region=gyeonggi|seoul&id=   그 정류장의 노선
```

피드(키는 맥미니 `.env`, compose 가 넘김):
- 경기 버스 - 공공데이터포털 `DATA_GO_KR_KEY`(일반 인증키, 인코딩된 키든 아니든): `busarrivalservice/v2/getBusArrivalListv2`
  (`predictTimeSec1/2`, 빈자리 `remainSeatCnt1/2`, 혼잡도 `crowded1/2`), `busstationservice/v2`(주변 500m·이름/번호 검색·경유
  노선). 빈자리는 -1 이면 정보 없음이고, 0 은 좌석형 노선(`routeTypeCd` 11·12·14·16·21·22·41·42·51·52)에서만 만석이다 - 일반·마을
  버스는 늘 0 또는 -1 이다. 서울 정류장도 경기 노선이 서면 여기에 나오고, 빈자리는 여기서만 나온다.
- 서울 버스 - 같은 키로 `ws.bus.go.kr/api/rest/stationinfo`(`getStationByUid` 의 `arrmsg1/2` "5분12초후[3번째 전]",
  `getStationByPos` 반경 500m, `getStationByName`). `resultType=json` 이 안 먹으면 기본 XML 을 읽는다. `rerdieDiv` 가 1 이면 좌석
  버스로 `remndrNmpr` 가 빈자리고, 4 면 입석 버스로 `congestion`(3 여유, 4 보통, 5 혼잡, 6 매우혼잡)을 읽는다. 0·2 는 정보가 없다
  (이 정류장을 지나는 경기 노선이 그렇다).
- 서울 지하철 - 서울 열린데이터광장 실시간 지하철 키 `SEOUL_SUBWAY_KEY`: `realtimeStationArrival`(`barvlDt` 초를 `recptnDt`
  부터, 0 이면 "[2]번째 전역" 같은 문구로 짐작 - `rough`). 수도권 19개 노선의 역이 다 나온다 - 코레일 구간의 경기도 역
  (수원·성균관대·병점·의왕, 수인분당선·신분당선·경강선의 경기 역)도 열차가 다닐 때 실시간이 온다(2026-10-24 12시 확인.
  열차가 없는 새벽에는 강남도 '데이터 없음'이다).
- 지하철역 - 바이너리에 담은 역 목록(`internal/transit/stations.json`, 561역): 피드가 아는 정확한 이름(`천호(풍납토성)`·
  `총신대입구(이수)`·`신촌(경의중앙선)` - 피드는 괄호 없는 `천호`로는 답하지 않는다), 좌표, 노선(`subwayId`)마다 이웃 역.
  이름으로 찾을 때는 괄호 앞(천호)과 괄호 안(이수)으로도 찾고, 보일 때는 "천호역 (풍납토성)". 이름·근처(1.5km)로 역을 찾고 노선은 '2호선 · 역삼 방면'처럼 다음 역으로 고른다(`line.id`
  `1002>역삼`) - 피드에 기대지 않으니 열차가 없는 밤에도 된다(전에는 실시간 피드가 아는 역만, 근처는 Overpass 라 밤엔
  지하철이 안 보이고 근처 찾기가 28초씩 걸렸다). 도착한 열차는 `trainLineNm` 의 'X방면'(다음 정차역)으로 가른다: X 가 경로의
  다음 역이거나 그 너머(급행)면 탄다, 반대쪽·다른 갈래(5호선 강동의 길동/둔촌동)는 아니다, 목록에 없는 역이면 같은 방향
  (`updnLine`)으로 확인된 열차를 따른다. 급행도 'X방면'은 바로 다음 역이다("천안행 - 화서방면 (급행)"). 목록은 `stations.py`
  가 피드의 `realtimeStationArrival/0/1000/ALL`(모든 역의 도착, 쪽당 1000건)에서 만든다: 각 열차의 이전·다음 역 id
  (`statnFid`·`statnTid`)가 이웃이고 이름은 피드 그대로. 좌표만 위키데이터(P625, 이름·노선으로 맞춤). 그 순간 열차가 없는
  구간(광명·서동탄 셔틀)은 빠지므로 운행 시간에 몇 분 간격으로 여러 번 받아 합친다(맥미니에서, 키는 `.env`):
  `SEOUL_SUBWAY_KEY=… python3 stations.py --fetch 8 --every 360 --save feed.jsonl stations.json 강남 수원`,
  받아 둔 것으로는 `python3 stations.py --feed feed.jsonl stations.json`.

공공데이터포털 키는 계정에 하나지만 서비스마다 활용신청해야 받아 준다(안 한 서비스는 '등록되지 않은 서비스키'). 이 코드가
부르는 것은 셋 - `경기도_버스도착정보 조회`(15080346), `경기도_정류소 조회`(15080666), `서울특별시_정류소정보조회 서비스`
(15000303), 모두 자동승인이고 승인 뒤 한 시간쯤 지나야 키가 먹는다. `서울특별시_버스도착정보조회 서비스`나 `B551982/rte`
(노선·버스 위치)는 쓰지 않는다. 미등록이면 그 경로의 `error`·정류장 찾기의 `missed` 가 어느 서비스를 신청할지 말한다 - 정류장
찾기는 피드 셋을 함께 묻고, 일부만 답하면 찾은 것에 빠진 것과 그 이유(`missed`)를 붙인다.

정류장 찾기(`Feeds.Search`): 두 버스 피드는 이름을 적힌 그대로만 찾고, 한 정류장을 서로 다르게 적는다(`강남역.홍우빌딩앞`,
`강남역홍우빌딩앞`). 못 찾은 피드는 띄어쓰기와 점을 뺀 이름으로 다시 묻고, 이름이 바뀐 정류장은 지금 이름으로도 묻는다(`renamed`:
나라빌딩 → 홍우빌딩, 정류소 번호 22997. 지도와 노선 안내에는 옛 이름이 남아 있다 - `was` 로 표시). 서울 피드만 찾은 정류장은 번호로
경기 피드에서 짝을 찾는다(6곳까지). 아무것도 없으면 이름의 일부(역 이름과 그 뒤)로 찾고 `like` 로 표시한다.

같은 정류장은 25초 동안 한 번만 묻는다(키의 하루 호출 한도). 키가 없으면 그 경로는 `stage: off` 에 이유를 실어 보내고
화면은 그대로 쓴다.

알림(`nudge`, 매분): 알림을 켠 경로가 그 요일·시간대 안이면 나가기 15분 안에 '슬슬 짐 싸야 해요', 2분 안(1분 늦음까지)
'지금 나가요'를 그 사람에게(`transit.pack|go`, `audience` 운영자·손님, `tier now`) - 차 한 대(차량 번호)와 단계마다 한
번. 시간대 밖에는 피드도 묻지 않는다. notify-svc 는 이 종류를 리마인드처럼 방해 금지에도 울린다.

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
실행 경로가 없어서(레지스트리가 모으기만 한다) 쓰지 않았다: 에이전트는 기록 스트림(`gocommon/queue`)으로 받고,
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
- 두 폰의 관리(`phones_test.go`, 2026-10-23): 집을 나서는 전환 메시지에 바로 Move, 같은 자리의 위치가 Significant 로
  되돌리지 않고(예전 판정으로 돌리면 이 테스트가 실패하는 것까지 확인), 나와서 한자리에 15분이면 Significant - 운영자·상대 둘
  다. 배터리 15% 면 Move 대신 Significant 에 `downgraded`, 충전 중이면 Move. 하루 첫 보고에 설정 여섯 개와 목록 요청, 같은
  날 두 번은 아님, 여기 영역은 바로 50m, 이름 붙인 점은 바로 50m 영역으로, Here-… 는 무시, 손으로 바꾼 반경은 다음 날 목록에서
  `rid` 그대로 50m 로. 위치 등록: 토큰·이름·좌표 검사, 목록을 모르면 목록부터 묻고 폰의 같은 이름 영역을 그 `rid` 로 옮기며,
  목록에 보이면 끝, 목록을 알면 바로 넣는 링크, 있는 곳에 만든 상대 영역은 도착 알림 없이 출발만. 따라다니는 영역
  (2026-10-26): 목록에 없으면 그 폰의 마지막 위치에 50m 로(위치를 모르면 아직), 있으면(`+` 로 시작하는 어떤 이름이든)
  그대로 - 50m 로 되돌리지도, 영역으로 남기지도 않고 그 진입은 아무것도 바꾸지 않음, 예전 하루 점검을 한 폰은 그날 한 번 더,
  두 폰 모두. 기능을 하나씩 빼면 이 테스트가 각각 실패하는 것까지 확인. 처음 Move 로 바꾸는 답에 `reportLocation`, 폰이
  받지 않아 다시 보내는 Move 에는 없음(두 폰, `place.Tell`).
- **실제 OwnTracks 앱으로 따라다니는 영역**(2026-10-26, 시뮬레이터, 로컬 서버): 하루 첫 보고의 목록 요청에 앱이 '집'만
  보내자 서버가 `+follow` 를 넣었고, 다음 목록에 `+follow:50:follow` 가 마지막 보고 자리에 있었다. 등록 영역 밖 B 에서
  Significant(손으로 건 모드)로 보고한 뒤 400m 떨어진 C 로 옮기자 앱이 스스로 `t: C` 위치를 1초 안에 보냈다. 그다음
  Move 와 `reportLocation` 을 한 답에 싣자 Move 로 바뀐 1초 뒤 `t: r`, `m: 2` 로 새 시각의 위치가 왔다(그 답을 부른
  'Send location now' 보고는 1분 전 위치).
- 출발(`internal/transit`·`transit_test.go`): 나갈 때와 말(여유~지금 나가요, 2분 늦음까지 뛰기, 3분 늦으면 다음 차),
  요일·시간대(자정 넘김 포함), 공식 문서 모양대로의 가짜 응답으로 경기 버스(초 단위, 하나만 온 목록, 결과 없음, 키 미등록,
  이스케이프된 키를 한 번만), 서울 버스(JSON·XML, "곧 도착"·"n분m초후"), 서울 지하철(받은 시각+초, 문구로 짐작, 떠난
  열차 빼기, 목록에도 피드에도 없는 역), 근처(세 피드 합치기, 하나가 실패해도), 역 목록(피드 이름·괄호 별칭으로 찾기,
  다음 역으로 방향, 급행·갈래·순환선 가르기). API: 사람별 경로·검사·손님 분리·피드 실패 표시·근처(me)·
  이름·노선, 시간대 안에서만 단계마다 한 번 알림(손님에게).
- 폰 설정 링크(Postgres 흐름 테스트): 토큰 없이 401, inline 을 풀면 HTTP 모드·URL·계정·Significant·cmd·remoteConfiguration
  과 매일 되돌리는 설정 여섯 개.
  waypoints 로 영역이 들어오고 여기서 정한 종류·동네는 남는다. 수집기의 배달앱 알림 줄 읽기(다시 넣기·다른 앱은 무시).
