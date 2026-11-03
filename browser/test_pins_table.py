"""pin.py 의 키패드 설정(SITES)이 결제 비밀번호의 한 표(gocommon/pay/pins.json)와 같은 결제를 가리키는지 본다.

    python3 -m unittest test_pins_table   (표준 라이브러리만 - 저장소 안에서. 브라우저 이미지에는 표가 없어 건너뛴다)

보관함(vault-svc)과 에이전트는 그 표를 읽고, pin.py 는 키패드마다 다른 설정(화면 글자, 옛 환경변수) 때문에
제 목록을 가진다. 둘이 어긋나면 저장한 비밀번호를 키패드가 찾지 못한다.
"""

import ast
import json
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TABLE = os.path.join(HERE, "..", "gocommon", "pay", "pins.json")


def pin_hosts():
    tree = ast.parse(open(os.path.join(HERE, "pin.py"), encoding="utf-8").read())
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(getattr(t, "id", "") == "SITES" for t in node.targets):
            return sorted(ast.literal_eval(node.value)[i]["host"] for i in range(len(node.value.elts)))
    raise AssertionError("pin.py 에 SITES 가 없습니다")


@unittest.skipUnless(os.path.exists(TABLE), "저장소 밖(브라우저 이미지)에는 표가 없다")
class OneTable(unittest.TestCase):
    def test_the_keypads_are_the_tables(self):
        table = sorted(p["host"] for p in json.load(open(TABLE, encoding="utf-8"))["pins"])
        self.assertEqual(pin_hosts(), table)


if __name__ == "__main__":
    unittest.main()
