import type { Allocation, Bucket, Drift, Holding, Move } from "./types.js";

/**
 * The allocation the portfolio is supposed to have, and which bucket each
 * holding counts toward.
 *
 * The brokerages only know "coin" and "stock", so the split between a growth
 * bet, a dividend payer and a gold ETF is ours to make. Listed symbols are the
 * exceptions; any other share counts as growth and any coin as a coin.
 *
 * 웹에 있던 것을 옮겨왔다. 화면과 에이전트가 같은 답을 내야 해서다 - 둘이
 * 각자 계산하면 "목표까지 얼마"가 두 개가 되고, 새 배당주를 한쪽에만 적는
 * 순간 서로 다른 말을 한다. 새 배당·금 종목을 사면 여기에 추가한다.
 */
export const BUCKETS: { id: Bucket; label: string; target: number }[] = [
  { id: "growth", label: "유망주", target: 0.45 },
  { id: "coin", label: "메이저코인", target: 0.3 },
  // Meant to be dollars, but won cash counts here too - there is no other
  // bucket for it - so the label does not promise dollars.
  { id: "cash", label: "현금", target: 0.12 },
  { id: "dividend", label: "배당주", target: 0.08 },
  { id: "gold", label: "금", target: 0.05 },
];

const DIVIDEND = new Set([
  "KO", "PEP", "PG", "JNJ", "O", "MO", "PM", "ABBV", "VZ", "T",
  "SCHD", "VYM", "DGRO", "JEPI", "JEPQ", "HDV", "SPYD", "DIVO",
  "458730", // TIGER 미국배당다우존스
  "446720", // SOL 미국배당다우존스
]);

const GOLD = new Set([
  "GLD", "IAU", "GLDM", "SGOL", "AAAU", "IAUM",
  "411060", // ACE KRX금현물
  "132030", // KODEX 골드선물(H)
  "M04020000", // KRX 금시장 금 99.99_1kg
]);

/** Short-term Treasury funds are parked dollars, not a stock position. */
const CASH_LIKE = new Set(["SGOV", "BIL", "SHV", "USFR", "TFLO", "BILS"]);

export function bucketOf(h: Holding): Bucket {
  if (h.kind === "coin") return "coin";
  if (h.kind === "gold") return "gold";
  if (GOLD.has(h.symbol)) return "gold";
  if (DIVIDEND.has(h.symbol)) return "dividend";
  if (CASH_LIKE.has(h.symbol)) return "cash";
  return "growth";
}

/**
 * How far a bucket may drift before it is worth trading back. ±7%p, but never
 * more than half the target - otherwise a 5% gold slot would read as "fine"
 * while holding no gold at all.
 */
export function bandOf(target: number): number {
  return Math.min(0.07, target / 2);
}

export function driftOf(holdings: Holding[], cashKrw: number): {
  rows: Drift[];
  totalKrw: number;
} {
  const value = new Map<Bucket, number>(BUCKETS.map((b) => [b.id, 0]));
  const symbols = new Map<Bucket, string[]>(BUCKETS.map((b) => [b.id, []]));
  for (const h of holdings) {
    const b = bucketOf(h);
    value.set(b, (value.get(b) ?? 0) + h.valueKrw);
    symbols.get(b)!.push(h.symbol);
  }
  value.set("cash", (value.get("cash") ?? 0) + cashKrw);

  const totalKrw = [...value.values()].reduce((sum, v) => sum + v, 0);
  const rows = BUCKETS.map((b) => {
    const valueKrw = value.get(b.id) ?? 0;
    const current = totalKrw > 0 ? valueKrw / totalKrw : 0;
    return {
      id: b.id,
      label: b.label,
      valueKrw,
      current,
      target: b.target,
      gapKrw: b.target * totalKrw - valueKrw,
      inBand: Math.abs(current - b.target) <= bandOf(b.target),
      symbols: symbols.get(b.id) ?? [],
    };
  });
  return { rows, totalKrw };
}

/**
 * The gaps, turned into transfers: what is over target pays for what is
 * under, largest first. Rebalancing is "move this from there to here", and a
 * list of separate buy and sell amounts leaves that matching to the reader.
 */
export function movesOf(rows: Drift[], minKrw = 10_000): Move[] {
  const over = rows
    .filter((r) => r.gapKrw < 0)
    .map((r) => ({ row: r, left: -r.gapKrw }))
    .sort((a, b) => b.left - a.left);
  const under = rows
    .filter((r) => r.gapKrw > 0)
    .map((r) => ({ row: r, left: r.gapKrw }))
    .sort((a, b) => b.left - a.left);

  const moves: Move[] = [];
  for (const need of under) {
    for (const give of over) {
      if (need.left <= 0) break;
      const amount = Math.min(need.left, give.left);
      if (amount <= 0) continue;
      need.left -= amount;
      give.left -= amount;
      if (amount < minKrw) continue;
      moves.push({
        from: give.row.id,
        to: need.row.id,
        amountKrw: amount,
        optional: need.row.inBand,
      });
    }
  }
  return moves;
}

/**
 * Fixed assets (주택청약) are left out on purpose: money that cannot be moved
 * is not something to rebalance, and counting it would bend every target.
 */
export function allocationOf(holdings: Holding[], cashKrw: number): Allocation {
  const { rows, totalKrw } = driftOf(holdings, cashKrw);
  return { rows, moves: movesOf(rows), totalKrw };
}
