#!/bin/bash
# 메시지 앱 데이터베이스를 Docker 가 읽을 수 있는 곳으로 복사한다.
#
# ~/Library/Messages 는 macOS 개인정보 보호 대상이라 Docker Desktop 은 전체
# 디스크 접근 권한 없이는 마운트하지 못한다. SSH(원격 로그인)는 그 권한을 갖고
# 있으므로, 이 스크립트를 맥 자신에게 SSH 로 실행해(LaunchAgent
# com.jarvis.imessage-mirror) 바뀐 파일만 2초마다 복사한다.
#
# 복사본은 ~/.jarvis-mirror (700) 에만 둔다 - 보호 밖으로 꺼낸 것이므로 다른
# 사용자·앱이 읽지 못하게 디렉터리 권한으로 막는다.
set -u
OUT="${JARVIS_MIRROR_DIR:-$HOME/.jarvis-mirror}"
mkdir -p "$OUT/messages" "$OUT/addressbook"
chmod 700 "$OUT"

copy() { # src dst
  local src=$1 dst=$2
  [ -f "$src" ] || { rm -f "$dst"; return; }
  # 크기와 수정 시각이 같으면 건너뛴다.
  if [ -f "$dst" ] && [ "$(stat -f '%z %m' "$src")" = "$(stat -f '%z %m' "$dst")" ]; then return; fi
  cp -p "$src" "$dst.tmp" && mv -f "$dst.tmp" "$dst"
}

while true; do
  for f in chat.db chat.db-wal; do
    copy "$HOME/Library/Messages/$f" "$OUT/messages/$f"
  done
  for f in AddressBook-v22.abcddb AddressBook-v22.abcddb-wal; do
    copy "$HOME/Library/Application Support/AddressBook/$f" "$OUT/addressbook/$f"
  done
  sleep 2
done
