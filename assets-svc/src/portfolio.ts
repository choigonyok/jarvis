import { changesFor } from "./change.js";
import * as db from "./db.js";
import { allocationOf } from "./target.js";
import type { CashLine, FixedAsset, Holding, Portfolio, Principal, PrincipalPart, Venue } from "./types.js";
import { kisGoldAccount, kisHoldings, upbitFlows, upbitHoldings, usdKrwRate } from "./venues.js";
import { CLOSED_TTL_MS, OPEN_TTL_MS, UPBIT_TTL_MS, krxOpen, memo, usOpen } from "./freshness.js";

// Each venue's answer, reused for as long as its numbers can actually have
// moved - see freshness.ts. The exchange rate rides with Upbit (it is the
// USDT market).
const rate = memo(() => UPBIT_TTL_MS, usdKrwRate);
const upbitCached = memo(() => UPBIT_TTL_MS, upbitHoldings);
let lastRate = 0;
const kisCached = memo(
  () => (krxOpen() || usOpen() ? OPEN_TTL_MS : CLOSED_TTL_MS),
  () => kisHoldings(lastRate),
);
const goldCached = memo(() => (krxOpen() ? OPEN_TTL_MS : CLOSED_TTL_MS), kisGoldAccount);
const flowsCached = memo(() => CLOSED_TTL_MS, () => upbitFlows(PRINCIPAL_SINCE));

/** Drop every cached venue answer: a trade or a principal entry was just recorded. */
export function invalidateVenues(): void {
  for (const m of [upbitCached, kisCached, goldCached, flowsCached]) m.clear();
}

/**
 * 조회할 곳이 없는 고정 자산(주택청약 등). 더 넣지도 빼지도 않을 돈이라 값이
 * 고정이다. 원금에도 같은 금액이 들어가므로 수익은 0으로 남고, 비중 계산에서는
 * 빠진다 - 옮길 수 없는 돈을 리밸런싱 대상으로 세면 목표가 왜곡된다.
 *
 * 금액은 개인 정보라 코드가 아니라 환경변수에 둔다(공개 저장소):
 *   FIXED_ASSETS='[{"id":"housing-subscription","label":"주택청약","valueKrw":1000000}]'
 * 형식이 틀리면 없는 것으로 보고 로그를 남긴다 - 자산 조회 전체를 멈출 일은 아니다.
 */
function fixedAssets(): FixedAsset[] {
  const raw = process.env.FIXED_ASSETS?.trim();
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) throw new Error("배열이 아닙니다");
    return parsed.map((f, i) => {
      const { id, label, valueKrw } = f as Record<string, unknown>;
      if (typeof label !== "string" || typeof valueKrw !== "number" || !Number.isFinite(valueKrw)) {
        throw new Error(`${i}번째 항목에 label·valueKrw 가 없습니다`);
      }
      return { id: typeof id === "string" ? id : `fixed-${i}`, label, valueKrw };
    });
  } catch (e) {
    console.error("FIXED_ASSETS 를 읽지 못해 고정 자산 없이 계산합니다:", (e as Error).message);
    return [];
  }
}
const FIXED_ASSETS = fixedAssets();
const fixedKrw = FIXED_ASSETS.reduce((sum, f) => sum + f.valueKrw, 0);

/** The day principal is counted from. Everything held then counts as put in. */

export const PRINCIPAL_SINCE = process.env.PRINCIPAL_SINCE?.trim() || "2026-05-01";

/**
 * Both brokerages, added up once.
 *
 * A venue that fails does not take the answer down with it: what did answer is
 * returned, and what did not is named. A portfolio that silently drops a
 * holding is worse than one that admits a gap - the total would be wrong and
 * nothing would say so.
 */
export async function buildPortfolio(): Promise<Portfolio> {
  const usdKrw = await rate.get();
  // A dollar balance cached at another rate would disagree with the page.
  if (Math.abs(usdKrw - lastRate) / (lastRate || 1) > 0.002) kisCached.clear();
  lastRate = usdKrw;
  const problems: string[] = [];

  const [upbit, kis, gold, principalFlows] = await Promise.all([
    upbitCached.get().catch((e: Error) => {
      problems.push(`업비트: ${e.message}`);
      return { holdings: [] as Holding[], cashKrw: 0, cash: [] as CashLine[], problems: [] as string[] };
    }),
    kisCached.get().catch((e: Error) => {
      problems.push(`한국투자증권: ${e.message}`);
      return { holdings: [] as Holding[], cashKrw: 0, cash: [] as CashLine[], problems: [] as string[] };
    }),
    goldCached.get().catch((e: Error) => {
      problems.push(`금현물: ${e.message}`);
      return undefined;
    }),
    Promise.all([
      flowsCached.get().catch((e: Error) => {
        problems.push(`업비트 입출금 내역: ${e.message}`);
        return null;
      }),
      db.manualFlows(PRINCIPAL_SINCE).catch((e: Error) => {
        problems.push(`원금 기록: ${e.message}`);
        return null;
      }),
    ]),
  ]);

  // Airdrop dust - a ten-millionth of a token worth nothing - is not a
  // position. It would take a row, a slice of the allocation bar and a line
  // on the baseline chart to say "zero".
  const DUST_KRW = 100;
  problems.push(...upbit.problems, ...kis.problems);

  const holdings = [...upbit.holdings, ...kis.holdings, ...(gold?.holdings ?? [])]
    .filter((h) => h.valueKrw >= DUST_KRW || h.costKrw >= DUST_KRW)
    .sort((a, b) => b.valueKrw - a.valueKrw);
  const cashKrw = upbit.cashKrw + kis.cashKrw + (gold?.cashKrw ?? 0);
  const invested = holdings.reduce((sum, h) => sum + h.valueKrw, 0);
  const costKrw = holdings.reduce((sum, h) => sum + h.costKrw, 0);

  // Priced against history, so this is how the basket moved rather than how
  // much money went in or out.
  const { changes, series, holdingSeries } = await changesFor(holdings, usdKrw).catch(() => ({
    changes: {
      day: { rate: null, amountKrw: null, missing: [] },
      month: { rate: null, amountKrw: null, missing: [] },
      year: { rate: null, amountKrw: null, missing: [] },
    },
    series: { day: [], month: [], year: [] },
    holdingSeries: {},
  }));
  const cash = [...upbit.cash, ...kis.cash, ...(gold?.cash ?? [])].sort((a, b) => b.valueKrw - a.valueKrw);

  // A principal missing either half would be wrong with nothing saying so -
  // the failure is already in `problems`, and the figure is withheld.
  const total = invested + cashKrw + fixedKrw;
  const [auto, manual] = principalFlows;
  let principal: Principal | null = null;
  if (auto && manual) {
    problems.push(...auto.problems);
    const flows = [...auto.flows, ...manual].sort((a, b) =>
      a.date === b.date ? a.id.localeCompare(b.id) : a.date.localeCompare(b.date),
    );
    const principalKrw = flows.reduce((sum, f) => sum + f.amountKrw, 0) + fixedKrw;

    // Each account against what went into that account. A transfer from one
    // to the other shows as out of one and into the other, so the parts still
    // add up to the whole.
    const valueOf: Record<Venue, number | null> = {
      kis: holdings
        .filter((h) => h.venue === "kis")
        .reduce((sum, h) => sum + h.valueKrw, 0) + kis.cashKrw,
      upbit: holdings
        .filter((h) => h.venue === "upbit")
        .reduce((sum, h) => sum + h.valueKrw, 0) + upbit.cashKrw,
      // null: no key configured. undefined: configured but failed - also
      // unknown, and already named in `problems`.
      gold: gold
        ? holdings
            .filter((h) => h.venue === "gold")
            .reduce((sum, h) => sum + h.valueKrw, 0) + gold.cashKrw
        : null,
      other: null,
    };
    const parts: PrincipalPart[] = (["kis", "upbit", "gold"] as const).map((venue) => {
      const p = flows
        .filter((f) => f.venue === venue)
        .reduce((sum, f) => sum + f.amountKrw, 0);
      const value = valueOf[venue];
      return {
        venue,
        valueKrw: value,
        principalKrw: p,
        profitKrw: value === null ? null : value - p,
        rate: value === null || p <= 0 ? null : (value - p) / p,
      };
    });

    principal = {
      since: PRINCIPAL_SINCE,
      principalKrw,
      profitKrw: total - principalKrw,
      rate: principalKrw > 0 ? (total - principalKrw) / principalKrw : 0,
      parts,
      flows,
    };
  }

  return {
    holdings,
    cashKrw,
    cash,
    goldGramKrw: gold?.gramKrw ?? null,
    holdingSeries,
    totalKrw: total,
    costKrw,
    profitKrw: invested - costKrw,
    returnRate: costKrw > 0 ? (invested - costKrw) / costKrw : 0,
    usdKrw,
    changes,
    series,
    at: new Date().toISOString(),
    principal,
    fixed: FIXED_ASSETS,
    allocation: allocationOf(holdings, cashKrw),
    problems,
  };
}
