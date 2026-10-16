#!/bin/sh
set -e

# docker compose restart는 컨테이너 파일시스템을 지우지 않는다. 지난 번
# Xvfb가 남긴 락이 그대로 있으면 "Server is already active for display 99"로
# 죽고, 재시작 루프가 된다.
rm -f /tmp/.X99-lock
rm -f /tmp/.X11-unix/X99 # 디렉터리는 이미지가 root 로 만들어 두었다

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

# 사람이 최초 로그인과 본인인증을 직접 하는 창구. VNC 서버는 이 컨테이너
# 안에서만 듣고(-localhost), 밖에서 오는 길은 websockify(6080) 하나다. 그 길로
# 와도 비밀번호(VNC_PASSWORD)가 있어야 화면이 열린다 - 같은 네트워크의 다른
# 컨테이너든, 게이트웨이의 입장권을 가진 쪽이든. 비밀번호가 없으면 창구를
# 열지 않는다(브라우저는 그대로 돈다). noVNC 페이지는 쓰지 않으므로 내보내지 않는다.
if [ -n "${VNC_PASSWORD:-}" ]; then
    x11vnc -storepasswd "$VNC_PASSWORD" /tmp/vncpass >/dev/null 2>&1
    x11vnc -display :99 -forever -shared -localhost -rfbauth /tmp/vncpass -quiet -bg
    websockify 6080 localhost:5900 &
else
    echo "VNC_PASSWORD 가 없어 원격 화면을 열지 않습니다" >&2
fi

# 결제로 나가는 요청만 멈춰 세우는 감시자. 별도 프로세스라 여기서 죽어도
# MCP는 계속 돈다 - 그리고 Fetch는 결제 패턴에만 걸려 있으므로, 죽더라도
# 일반 탐색은 막히지 않는다.
# 죽으면 다시 띄운다. 가드가 없는 동안 새 탭은 감시 없이 뜬다.
(while true; do
    python3 /app/guard.py
    echo "guard 가 끝났습니다(종료 코드 $?). 2초 뒤 다시 띄웁니다" >&2
    sleep 2
done) &

exec python3 /app/bridge.py
