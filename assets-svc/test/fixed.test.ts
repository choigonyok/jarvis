import assert from "node:assert/strict";
import { test } from "node:test";
import { fixedAssetsFrom, fixedOn, heldOn } from "../src/fixed.js";

// 주택청약 cancelled on 11/06: held through 11/05, so the snapshots up to then
// (which held it) still meet a principal that holds it, and from 11/06 it is
// in neither the total nor the principal nor the list.
test("a fixed asset given up counts through its last day", () => {
  const list = fixedAssetsFrom(
    '[{"id":"housing-subscription","label":"주택청약","valueKrw":1000000,"until":"2026-11-05"},{"label":"적금","valueKrw":500000}]',
  );
  assert.equal(fixedOn(list, "2026-10-01"), 1_500_000);
  assert.equal(fixedOn(list, "2026-11-05"), 1_500_000);
  assert.equal(fixedOn(list, "2026-11-06"), 500_000);
  assert.deepEqual(
    list.filter((f) => heldOn(f, "2026-11-06")).map((f) => f.label),
    ["적금"],
  );
  assert.equal(list[1].id, "fixed-1");
});

test("a list it cannot read is none, and says so", () => {
  for (const raw of [undefined, "", "  ", "{}", "[1]", '[{"label":"주택청약","valueKrw":"100"}]', '[{"label":"주택청약","valueKrw":100,"until":"11/05"}]']) {
    assert.deepEqual(fixedAssetsFrom(raw), [], String(raw));
  }
});
