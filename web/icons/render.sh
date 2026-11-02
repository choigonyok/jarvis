#!/bin/zsh
# Renders the app icon set from the two SVGs beside this script into
# public/icons and src/app/favicon.ico. Headless Chrome draws the SVG, filters
# included, at 1024, 512 and the favicon sizes; it does not lay out windows
# between roughly 50 and 500 px, so 180 and 192 are downscaled from the 1024
# render with sips. favicon.ico wraps the 16/32/48 PNGs as they are.
set -e
cd "$(dirname "$0")"
CH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
OUT=../public/icons
V=v3
TMP=$(mktemp -d)
shot() { # svg out size
  "$CH" --headless=new --no-sandbox --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
    --default-background-color=00000000 --user-data-dir="$TMP/profile" \
    --window-size=$3,$3 --screenshot="$2" "file://$PWD/$1" >/dev/null 2>&1
}
shot orb.svg "$TMP/orb-1024.png" 1024
shot orb.svg "$OUT/icon-512.$V.png" 512
cp "$OUT/icon-512.$V.png" "$OUT/icon-512-maskable.$V.png"
sips -z 180 180 "$TMP/orb-1024.png" --out "$OUT/apple-touch-icon.$V.png" >/dev/null
sips -z 192 192 "$TMP/orb-1024.png" --out "$OUT/icon-192.$V.png" >/dev/null
shot orb-favicon.svg "$OUT/favicon-32.$V.png" 32
shot orb-favicon.svg "$TMP/fav-16.png" 16
shot orb-favicon.svg "$TMP/fav-48.png" 48
python3 - "$TMP/fav-16.png" "$OUT/favicon-32.$V.png" "$TMP/fav-48.png" ../src/app/favicon.ico <<'PY'
import struct, sys
pngs = [open(p, "rb").read() for p in sys.argv[1:4]]
off = 6 + 16 * len(pngs); ents = b""; data = b""
for sz, b in zip((16, 32, 48), pngs):
    ents += struct.pack("<BBBBHHII", sz, sz, 0, 0, 1, 32, len(b), off + len(data)); data += b
open(sys.argv[4], "wb").write(struct.pack("<HHH", 0, 1, len(pngs)) + ents + data)
PY
rm -rf "$TMP"
