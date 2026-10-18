# tts

대화 탭 음성 모드에서 Jarvis 가 **듣는 사람 자신의 목소리**로 답합니다(운영자는 운영자의, 손님은
손님의). 녹음 한 번(대본 15초쯤)과 그 대본으로 Qwen3-TTS 1.7B Base 가 목소리를 복제해, 답을 문장
단위로 읽어 줍니다.

## 왜 compose 가 아니라 맥미니 호스트인가

MLX 는 Metal GPU 가 필요하고 컨테이너는 그것을 못 봅니다. 그래서 이 하나는 카톡 수집기처럼
맥미니 macOS 에서 LaunchAgent(`com.jarvis.tts`)로 돕니다. 게이트웨이(edge)는 터널 컨테이너에서
`host.docker.internal:8106` 으로 닿습니다(VPC 서비스 `jarvis-tts`). 토큰은 맥미니 `~/jarvis/.env` 의
`JARVIS_API_TOKEN`(운영자)과 `JARVIS_GUEST_API_TOKEN`(손님) 둘입니다.

```
브라우저(음성 모드) ─ /api/tts/* ─▶ web Worker ─▶ edge(/tts/*) ─▶ 터널 ─▶ host.docker.internal:8106 (이 서버)
```

녹음은 사람마다 하나씩이고, 어느 녹음을 읽고·바꾸고·지우고·그 목소리로 말할지는 요청이 들고 온
**토큰**으로만 정합니다 - 요청 내용으로는 바꿀 수 없습니다. 손님의 요청은 콘솔이 손님 토큰으로
게이트웨이에 보내고, 게이트웨이는 그것을 `JARVIS_GUEST_API_TOKEN` 으로만 넘깁니다(없으면 503 -
운영자 토큰으로 넘어가지 않습니다). 그래서 손님은 운영자의 목소리를 듣거나 바꾸거나 쓸 수 없고,
그 반대도 마찬가지입니다. `JARVIS_GUEST_API_TOKEN` 이 없거나 운영자 토큰과 같으면 손님 목소리는
꺼집니다(손님은 브라우저 내장 음성). 모델의 프롬프트 캐시는 녹음 자체(대본·소리)로 나뉘어서 두
목소리가 섞이지 않습니다.

## API

| 메서드 | 경로 | 하는 일 |
|---|---|---|
| GET | `/health` | 모델이 올라왔는지, 목소리가 있는지(`voice` 운영자, `voices` 둘 다). 토큰 불필요 |
| GET | `/voice` | 토큰 주인의 녹음: `{present, text, seconds, savedAt}` |
| PUT | `/voice` | `{"text": 대본, "audio": base64 WAV}` - 앞뒤 무음을 잘라 24kHz 모노로 저장하고 예열 |
| DELETE | `/voice` | 지운다(음성 모드는 브라우저 내장 음성으로 돌아간다) |
| POST | `/speak` | `{"text": …, "lang": "korean"}` → `audio/wav`, 토큰 주인의 목소리로. 한 번에 600자까지 |

녹음은 운영자 것이 `~/.jarvis-tts/voice.*`, 손님 것이 `~/.jarvis-tts/guest/voice.*` 입니다.

생성은 한 번에 하나입니다(모델이 하나이고, 둘을 동시에 돌리면 GPU 를 나눠 더 늦게 끝납니다).
콘솔이 답을 문장 단위로 끊어 보내고 다음 문장을 미리 받아 두므로, 첫 소리는 2초 안쪽에 나고
그 뒤는 끊기지 않습니다.

## 측정 (맥미니 M4 16GB, 2026-10-18)

| 항목 | 값 |
|---|---|
| 모델 올리는 시간 | 24초, 상주 3.2 GB |
| 한 문장(30~40자) 생성 | 음성 4.6~5.3초를 3.6~4.1초에 (RTF 0.78) |
| 생성 중 피크 메모리 | MLX 한도 없이 7.5 GB → `TTS_MEMORY_LIMIT_GB=3.8`(기본)로 5.3 GB |

맥미니는 Docker VM(약 3.4 GB)과 같이 16 GB 를 씁니다. 생성 때 스왑이 늘어나는 것은 받아들인
비용입니다. 더 줄여야 하면 `TTS_MODEL=mlx-community/Qwen3-TTS-12Hz-1.7B-Base-4bit` 로
`scripts/tts install` - 발음 정확도는 거의 같고 가중치가 1.2 GB 작습니다. 0.6B 로 내리지는
않습니다(발음 오류가 3배 가까이 많습니다).

## 운영

```
scripts/tts install    코드 보내기 → uv·Python 3.12·venv(~/.jarvis-tts/venv)·모델(~2GB) → LaunchAgent
scripts/tts status     돌고 있는지, /health, 마지막 로그
scripts/tts logs -f
scripts/tts restart    모델을 다시 올리므로 30초쯤
scripts/tts uninstall  녹음(~/.jarvis-tts/voice.*, guest/)과 모델 캐시는 남긴다
```

LaunchAgent 는 `HF_HUB_OFFLINE=1` 로 뜹니다 - 모델은 install 때 받아 두고, 부팅 때 네트워크를
기다리며 서지 않게. 로그는 `~/Library/Logs/jarvis-tts.log`.

## 콘솔 쪽

- `web/src/lib/use-speech.ts` - `useVoiceStatus`(녹음이 있는지), `useSpeech(lang, server)`:
  서버 음성은 `sentences()` 로 끊어 `/api/tts/speak` 를 문장마다 부르고 AudioContext 로 이어
  재생합니다. 링은 재생 중인 소리의 실제 스펙트럼을 그립니다. 서비스가 죽었거나 녹음이 없으면
  브라우저 내장 음성이 남은 부분을 이어 읽습니다 - 답이 소리 없이 사라지는 일은 없습니다.
- `web/src/components/chat/voice-setup.tsx` - 녹음 화면. 대본을 보여 주고 ScriptProcessor 로
  PCM 을 그대로 받아 16비트 WAV 로 올립니다(Safari 는 AAC, Chrome 은 Opus 를 내주고 맥미니에는
  디코더가 없어서). 들어보고 나서 저장합니다.
- 음성 모드 상단의 파형 아이콘이 이 화면으로 가는 문입니다. 등록되면 초록으로 표시됩니다.

## 하지 않은 것

- 스트리밍 생성(문장 안에서 부분 재생). 문장 단위 파이프라인으로 첫 소리가 충분히 빨라서.
- 생성 결과 캐시. 같은 문장을 다시 듣는 일이 드물어서.
- 상태 탭의 판정 항목. `/health` 는 있으니 status-svc 에 probe 하나 더하면 됩니다(`host.docker.internal:8106`).
