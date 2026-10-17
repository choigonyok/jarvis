"""pin 의 쿠페이 그림 키패드 읽기 - 숫자를 확실히 읽을 때만 돌려주는가.

    python -m unittest test_pin   (Pillow·websockets·aiohttp 가 있는 곳 - 브라우저 이미지)

SPRITE 는 2026-10-18 01:53 결제 화면(rocketpay.coupang.com)의 실제 스프라이트다. 키 배치만 담긴
그림이라 비밀번호와는 상관이 없다. 칸 순서(행 우선)로 5 9 8 4 / 1 2 6 7 / 0 3 이 그려져 있다.
"""

import base64
import io
import os
import unittest

os.environ.setdefault("PAYMENT_HOSTS_FILE", os.path.join(os.path.dirname(__file__), "payment-hosts.json"))

from PIL import Image  # noqa: E402

import pin  # noqa: E402

SPRITE = (
    "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"
)
# 키 N 이 보여 주는 칸: 쿠페이 키패드의 배경 위치(가로 25, 세로 26 간격)와 span 크기(17×26).
OFFSETS = [(0, 0), (25, 0), (50, 0), (75, 0), (0, 26), (25, 26), (50, 26), (75, 26), (0, 52), (25, 52)]
DRAWN = [5, 9, 8, 4, 1, 2, 6, 7, 0, 3]


def cells(src, offsets=OFFSETS, w=17, h=26):
    return [{"key": str(i), "src": src, "x": x, "y": y, "w": w, "h": h} for i, (x, y) in enumerate(offsets)]


def uri(img):
    buf = io.BytesIO()
    img.save(buf, "PNG")
    return "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()


class SpriteKeypad(unittest.TestCase):
    def setUp(self):
        self.src = "data:image/png;base64," + SPRITE
        self.sheet = Image.open(io.BytesIO(base64.b64decode(SPRITE))).convert("RGBA")

    def test_reads_the_real_keypad(self):
        self.assertEqual(pin.read_sprite(cells(self.src)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_reads_a_shuffled_one(self):
        # 그림 속 칸을 다른 순서로 다시 붙여, 배치가 바뀐 키패드를 만든다.
        order = [3, 0, 9, 1, 8, 2, 7, 4, 6, 5]
        shuffled = Image.new("RGBA", self.sheet.size, (0, 0, 0, 0))
        for slot, src in enumerate(order):
            sx, sy = OFFSETS[src]
            dx, dy = OFFSETS[slot]
            shuffled.paste(self.sheet.crop((sx, sy, sx + 25, sy + 26)), (dx, dy))
        got = pin.read_sprite(cells(uri(shuffled)))
        self.assertEqual(got, {str(slot): DRAWN[src] for slot, src in enumerate(order)})

    def test_a_pixel_off_still_reads(self):
        shifted = [(x + 1, y) for x, y in OFFSETS[:5]] + [(x, y + 1) for x, y in OFFSETS[5:]]
        self.assertEqual(pin.read_sprite(cells(self.src, shifted)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_refuses_what_it_cannot_read(self):
        # 빈 칸(오른쪽 아래), 같은 숫자 두 번, 반쯤 잘린 숫자 - 모두 아무것도 누르지 않는다.
        for offsets in [OFFSETS[:9] + [(75, 52)], OFFSETS[:9] + [OFFSETS[0]]]:
            with self.assertRaises(RuntimeError):
                pin.read_sprite(cells(self.src, offsets))
        with self.assertRaises(RuntimeError):
            pin.read_sprite(cells(self.src, w=8))


if __name__ == "__main__":
    unittest.main()
