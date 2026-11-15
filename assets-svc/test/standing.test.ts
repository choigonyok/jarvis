import assert from "node:assert/strict";
import { test } from "node:test";

import type { Standing } from "../src/db.js";
import { cancelStanding } from "../src/standing.js";

// 10:00 in New York, Friday 13 November 2026: the session is on.
const now = new Date("2026-11-13T15:00:00Z");
const waiting: Standing = {
  id: 3, createdAt: "2026-11-10T14:00:00.000Z", venue: "kis", symbol: "KO", side: "buy", units: 2, limitPrice: 60,
  heldBefore: 0, reason: "", status: "active", placedOn: "2026-11-12", brokerId: "0001", message: "", expiresOn: null, doneAt: null,
};
const outToday: Standing = { ...waiting, placedOn: "2026-11-13", brokerId: "0002" };

test("a cancel says as a value that nothing of the order is left", async () => {
  const asked: string[] = [];
  const broker = async (o: Standing) => {
    asked.push(o.brokerId!);
  };
  // Yesterday's order went with its session: the broker is not asked.
  assert.deepEqual(await cancelStanding(waiting, now, broker), { cancelled: true, message: "취소했어요" });
  assert.deepEqual(asked, []);
  assert.deepEqual(await cancelStanding(outToday, now, broker), { cancelled: true, message: "취소했어요 (오늘 낸 주문도 취소)" });
  assert.deepEqual(asked, ["0002"]);
});

test("a broker that will not cancel today's order makes it not cancelled, and the words say what is left", async () => {
  const out = await cancelStanding(outToday, now, async () => {
    throw new Error("APBK0918 이미 체결된 주문입니다");
  });
  assert.deepEqual(out, {
    cancelled: false,
    message: "취소했어요. 오늘 낸 주문은 증권사에서 취소하지 못했어요: APBK0918 이미 체결된 주문입니다",
  });
});
