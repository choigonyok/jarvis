import assert from "node:assert/strict";
import { test } from "node:test";
import { accountOf, detailOf, halfRead, problem } from "../src/accounts.js";

test("문제는 한 표의 계좌 이름으로 쓰고 읽는다", () => {
  assert.equal(problem("upbit", "허용되지 않은 IP"), "업비트: 허용되지 않은 IP");
  assert.equal(accountOf(problem("upbitFlows", "권한 없음")), "upbitFlows");
  assert.equal(accountOf("업비트: 안 됨"), "upbit");
  assert.equal(accountOf("무엇: 안 됨"), undefined);
  assert.equal(detailOf(problem("gold", "키가 설정되지 않았습니다")), "키가 설정되지 않았습니다");
  assert.equal(halfRead([problem("kis", "만료")]), true);
  assert.equal(halfRead([problem("flows", "표를 읽지 못했습니다"), problem("upbitFlows", "권한 없음")]), false);
  assert.equal(halfRead(undefined), false);
});
