FROM python:3.13-slim-bookworm

# chromium: browser-use가 직접 띄우는 실제 브라우저. cdp-use로 CDP를 말하므로
#           playwright 설치는 필요 없다.
# xvfb/x11vnc/novnc: 화면 없는 컨테이너에 가상 디스플레이를 주고, 최초 로그인과
#           본인인증을 사람이 눈으로 보며 처리할 수 있게 웹으로 내보낸다.
# fonts-nanum: 없으면 한국어 사이트가 전부 네모로 보여서 로그인조차 못 한다.
RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium \
        xvfb x11vnc novnc websockify x11-utils \
        fonts-nanum fonts-liberation \
        ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY bridge.py guard.py upload.py entrypoint.sh /app/
COPY payment-hosts.json /app/payment-hosts.json
COPY config.json /config/browseruse/config.json

ENV DISPLAY=:99 \
    JARVIS_AGENT_URL=http://agent:8080/intercept \
    BROWSER_USE_CONFIG_DIR=/config/browseruse \
    PYTHONUNBUFFERED=1 \
    # 개인 비서가 여는 페이지 목록이 밖으로 나갈 이유가 없다.
    ANONYMIZED_TELEMETRY=false \
    BROWSER_USE_CLOUD_SYNC=false

# 로그인 상태가 사는 곳. 이 볼륨이 비면 처음부터 다시 로그인해야 한다.
VOLUME ["/profile"]

EXPOSE 8931 6080
ENTRYPOINT ["/app/entrypoint.sh"]
