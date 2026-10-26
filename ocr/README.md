# ocr

그림 한 장에 쓰인 글자를 읽는 작은 서버입니다. 애플 Vision(`VNRecognizeTextRequest`, 정확도 우선, ko-KR·en-US).
search-svc 가 페이지의 그림(공지 카드, 상품 상세 이미지, 포스터)을 조각내 여기로 보냅니다.

## 왜 맥미니 호스트인가

Vision 은 macOS 의 것이라 컨테이너(리눅스)에는 없습니다. 그래서 tts 처럼 이것 하나는 맥미니에서 LaunchAgent
(`com.jarvis.ocr`)로 돌고, search-svc 는 `host.docker.internal:8107` 로 닿습니다. `127.0.0.1` 에만 묶여서 같은
와이파이의 다른 기기는 이 포트를 보지 못합니다. 의존성이 없는 Swift 파일 하나라 맥미니의 `swiftc` 로 빌드합니다.

한국어 공지 카드·상세 이미지에서 무료로, 기기 안에서, 조각 하나에 0.25초 안팎으로 읽습니다. 컨테이너 안의 대안
(Tesseract, PaddleOCR)은 한국어 디자인 글씨에 약하거나 무겁습니다.

## API

| 메서드 | 경로 | 하는 일 |
|---|---|---|
| GET | `/health` | `{"ok":true}` - 토큰 불필요 |
| POST | `/ocr` | 본문 = 이미지 바이트 → `{"width","height","ms","lines":[{"text","conf"}]}` |

토큰은 `Authorization: Bearer <JARVIS_SEARCH_TOKEN>` - 환경변수가 없으면 `--env-file`(맥미니 `~/jarvis/.env`)에서 읽습니다.
한 번에 넷까지 읽습니다. 세로로 아주 긴 그림은 Vision 이 줄여서 보느라 글자를 놓치므로, 자르는 것은 보내는 쪽(search-svc)이 합니다.

## 운영

```
scripts/ocr install    ocr/main.swift 만 보내 맥미니에서 빌드(~/.jarvis-ocr/jarvis-ocr)하고 LaunchAgent 로 띄운다
scripts/ocr status     돌고 있는지, /health, 마지막 로그
scripts/ocr logs -f
scripts/ocr restart    .env 의 JARVIS_SEARCH_TOKEN 을 바꿨을 때
scripts/ocr uninstall
```

로그는 `~/Library/Logs/jarvis-ocr.log`. 로컬 시험: `swiftc -O -swift-version 5 ocr/main.swift -o /tmp/jarvis-ocr &&
JARVIS_SEARCH_TOKEN=dev /tmp/jarvis-ocr --port 18107`.
