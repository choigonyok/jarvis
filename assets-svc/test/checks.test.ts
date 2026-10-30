import assert from "node:assert/strict";
import { test } from "node:test";
import { venueChecks, worthSaying } from "../src/checks.js";

test("계좌마다 자기 문제만 본다", () => {
  const at = new Date("2026-10-31T06:00:00+09:00");
  const rows = venueChecks(["업비트: 허용되지 않은 IP", "원금 기록: 표를 읽지 못했습니다", "금현물: 키가 설정되지 않았습니다"], at);
  const by = Object.fromEntries(rows.map((r) => [r.id, r]));
  assert.equal(by["assets.upbit"].state, "fail");
  assert.equal(by["assets.upbit"].summary, "허용되지 않은 IP");
  assert.equal(by["assets.kis"].state, "ok");
  assert.equal(by["assets.kis"].observedAt, at.toISOString());
  assert.equal(by["assets.gold"].state, "unknown");
  assert.equal(by["assets.flows"].state, "warn");
});

test("바뀌었을 때와 한 시간에 한 번만 말한다", () => {
  const rows = venueChecks([], new Date(0));
  const now = 1_000_000;
  assert.equal(worthSaying(rows, now).length, 4);
  assert.equal(worthSaying(rows, now + 60_000).length, 0);
  assert.equal(worthSaying(venueChecks(["업비트: 안 됨"], new Date(0)), now + 120_000).length, 1);
  assert.equal(worthSaying(rows, now + 3_700_000).length, 4);
});
