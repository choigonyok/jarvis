#!/bin/sh
set -e

# docker compose restart는 컨테이너 파일시스템을 지우지 않는다. 지난 번
# Xvfb가 남긴 락이 그대로 있으면 "Server is already active for display 99"로
# 죽고, 재시작 루프가 된다.
rm -f /tmp/.X99-lock
rm -rf /tmp/.X11-unix

# Chrome은 프로필에 자기 컨테이너 호스트명으로 된 락을 남긴다. 컨테이너가
# 죽으면 그 호스트는 사라지는데 락은 볼륨에 남아서, 다음 기동 때 Chrome이
# "다른 인스턴스가 쓰는 중"으로 보고 CDP를 올리지 못한다. 프로필 하나당
# 브라우저 하나뿐인 구성이므로 지우고 시작하는 것이 맞다.
rm -f /profile/SingletonLock /profile/SingletonCookie /profile/SingletonSocket

# 가상 디스플레이. browser-use는 headless=false로 뜨므로 실제 창이 그려지고,
# 그 창을 VNC로 내보낸다. 헤드리스 플래그가 아예 없어서 헤드리스를 막는
# 사이트에도 덜 걸린다.
Xvfb :99 -screen 0 1440x900x24 -nolisten tcp &

# sleep으로 찍지 않는다. 느린 날에는 1초가 모자라고, 빠른 날에는 낭비다.
i=0
while ! xdpyinfo -display :99 >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 100 ]; then
        echo "Xvfb가 :99에 뜨지 않았습니다" >&2
        exit 1
    fi
    sleep 0.1
done

# 사람이 최초 로그인과 본인인증을 직접 하는 창구. 6080은 compose에서
# 127.0.0.1로만 묶는다.
x11vnc -display :99 -forever -shared -nopw -quiet -bg
websockify --web=/usr/share/novnc 6080 localhost:5900 &

# 결제로 나가는 요청만 멈춰 세우는 감시자. 별도 프로세스라 여기서 죽어도
# MCP는 계속 돈다 - 그리고 Fetch는 결제 패턴에만 걸려 있으므로, 죽더라도
# 일반 탐색은 막히지 않는다.
python3 /app/guard.py &

exec python3 /app/bridge.py
