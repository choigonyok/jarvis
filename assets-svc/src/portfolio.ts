import { changesFor } from "./change.js";
import * as db from "./db.js";
import { allocationOf } from "./target.js";
import { detectFlow, reconcile } from "./kis-flows.js";
import { COIN_TAX_FROM, replayKis, replayUpbit, salesFrom, summarize } from "./realized.js";
import type {
  Bridge,
  CashLine,
  FixedAsset,
  Holding,
  Portfolio,
  Principal,
  PrincipalPart,
  Realized,
  TrackPoint,
  Venue,
} from "./types.js";
import {
  goldSettlements,
  kisGoldAccount,
  kisHoldings,
  kisTrades,
  settlementsOf,
  upbitDailyCloses,
  upbitFills,
  upbitFlows,
  upbitHoldings,
  usdKrwRate,
} from "./venues.js";
import { CLOSED_TTL_MS, OPEN_TTL_MS, UPBIT_TTL_MS, krxOpen, memo, usOpen } from "./freshness.js";

// Each venue's answer, reused for as long as its numbers can actually have
// moved - see freshness.ts. Upbit's USDT price values USDT and is the
// fallback rate; KIS's own base rate values everything held at KIS.
const rate = memo(() => UPBIT_TTL_MS, usdKrwRate);
const upbitCached = memo(() => UPBIT_TTL_MS, upbitHoldings);
let lastRate = 0;
const kisCached = memo(
  () => (krxOpen() || usOpen() ? OPEN_TTL_MS : CLOSED_TTL_MS),
  () => kisHoldings(lastRate),
);
const goldCached = memo(() => (krxOpen() ? OPEN_TTL_MS : CLOSED_TTL_MS), kisGoldAccount);
const flowsCached = memo(() => CLOSED_TTL_MS, () => upbitFlows(PRINCIPAL_SINCE));

// Past sales change only when something is sold, so they ride the slow TTL;
// the refresh button (invalidateVenues) is how a sale just made shows up.
const thisYear = () => Number(new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }).slice(0, 4));
// One read of KIS's fills serves the realized returns (back to January, for
// the tax) and the cash checks (from the principal's start).
const kisTradesCached = memo(() => CLOSED_TTL_MS, () => kisTrades(salesFrom(PRINCIPAL_SINCE, thisYear())));
const goldSettlementsCached = memo(() => CLOSED_TTL_MS, () => goldSettlements(PRINCIPAL_SINCE));
const upbitTradesCached = memo(() => CLOSED_TTL_MS, async () => {
  // Coin gains are not taxed before COIN_TAX_FROM, so until then there is no
  // reason to walk Upbit back past the principal's start, a week per call.
  const year = thisYear();
  const from = year >= COIN_TAX_FROM ? salesFrom(PRINCIPAL_SINCE, year) : PRINCIPAL_SINCE;
  const fills = await upbitFills(from);
  const days = Math.ceil((Date.now() - Date.parse(from)) / 86_400_000) + 2;
  const closeAtStart = new Map<string, number>();
  for (const symbol of new Set(fills.map((f) => f.symbol))) {
    // Held on the first morning: the close the day before, as the ledger counts it.
    const close = (await upbitDailyCloses(symbol, days)).get(dayBefore(from));
    if (close) closeAtStart.set(symbol, close);
  }
  return { fills, closeAtStart };
});

function dayBefore(date: string): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() - 1);
  return d.toISOString().slice(0, 10);
}

/** Drop every cached venue answer: a trade or a principal entry was just recorded. */
export function invalidateVenues(): void {
  for (const m of [upbitCached, kisCached, goldCached, flowsCached, kisTradesCached, goldSettlementsCached, upbitTradesCached]) {
    m.clear();
  }
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
  const usdtKrw = await rate.get();
  // KIS falls back to this rate only without one of its own; a balance cached
  // at an older fallback would disagree with the page.
  if (Math.abs(usdtKrw - lastRate) / (lastRate || 1) > 0.002) kisCached.clear();
  lastRate = usdtKrw;
  const problems: string[] = [];

  const [upbit, kis, gold, upbitFlowsRead, kisFills, upbitTrades, goldSettled] = await Promise.all([
    upbitCached.get().catch((e: Error) => {
      problems.push(`업비트: ${e.message}`);
      return { holdings: [] as Holding[], cashKrw: 0, cash: [] as CashLine[], problems: [] as string[] };
    }),
    kisCached.get().catch((e: Error) => {
      problems.push(`한국투자증권: ${e.message}`);
      return {
        holdings: [] as Holding[],
        cashKrw: 0,
        cash: [] as CashLine[],
        settledCash: null as { krw: number; usd: number } | null,
        usdKrw: usdtKrw,
        problems: [] as string[],
      };
    }),
    goldCached.get().catch((e: Error) => {
      problems.push(`금현물: ${e.message}`);
      return undefined;
    }),
    flowsCached.get().catch((e: Error) => {
      problems.push(`업비트 입출금 내역: ${e.message}`);
      return null;
    }),
    kisTradesCached.get().catch((e: Error) => {
      problems.push(`한국투자증권 거래내역: ${e.message}`);
      return null;
    }),
    upbitTradesCached.get().catch((e: Error) => {
      problems.push(`업비트 체결 내역: ${e.message}`);
      return null;
    }),
    goldSettlementsCached.get().catch((e: Error) => {
      problems.push(`금현물 매매 내역: ${e.message}`);
      return null;
    }),
  ]);

  // The page's dollar rate is KIS's: almost every dollar held is held there.
  const usdKrw = kis.usdKrw;
  const settlements = kisFills ? settlementsOf(kisFills, PRINCIPAL_SINCE) : null;

  // KIS's sales on the won actually paid and received, and what is still held
  // at the won it cost - see replayKis. A holding whose quantity the replay
  // agrees with takes that cost; one it does not (bought before the fills
  // reach) keeps the dollar cost at today's rate.
  const kisReplay = kisFills ? replayKis(kisFills) : null;
  if (kisReplay) {
    problems.push(...kisReplay.problems);
    for (const h of kis.holdings) {
      const pos = kisReplay.held.get(h.symbol);
      if (pos && Math.abs(pos.quantity - h.quantity) < 1e-6 && pos.costKrw > 0) {
        h.costKrw = pos.costKrw;
        h.since = pos.since;
      } else if (!pos) h.since = null; // held since before the fills reach
    }
  }

  // Each KIS account's cash today against its last day's: what the settled
  // trades do not explain is a deposit or withdrawal, written into the ledger
  // before it is read below. Only with both halves - a guess from one would
  // be stored.
  const today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const accounts = [
    { venue: "kis" as const, cash: kis.settledCash, settlements, label: "한국투자증권" },
    { venue: "gold" as const, cash: gold ? { krw: gold.cashKrw, usd: 0 } : null, settlements: goldSettled, label: "금현물" },
  ];
  for (const a of accounts) {
    if (!a.cash || !a.settlements) continue;
    await detectFlow(a.venue, today, a.cash, a.settlements, usdKrw).catch((e: Error) => {
      problems.push(`${a.label} 입출금 감지: ${e.message}`);
    });
  }
  const principalFlows = [
    upbitFlowsRead,
    await db.manualFlows(PRINCIPAL_SINCE).catch((e: Error) => {
      problems.push(`원금 기록: ${e.message}`);
      return null;
    }),
  ] as const;

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
  // What holding dollars at KIS cost or made; null when it could not be replayed.
  let kisFxKrw: number | null = null;
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
      checks: [],
    };
    for (const a of accounts) {
      if (!a.cash || !a.settlements) continue;
      const r = reconcile(a.venue, flows, a.settlements, a.cash, usdKrw);
      principal.checks.push(...r.checks);
      if (a.venue === "kis") kisFxKrw = r.fxKrw;
    }
  }

  // Upbit's sales need what is held now to know what was held at the start.
  // A venue whose balance failed has no such answer, and its replay would be
  // wrong - so it is left out, which `problems` already explains.
  let realized: Realized | null = null;
  if (kisReplay || upbitTrades) {
    const sales = [...(kisReplay?.sales ?? [])];
    const missing: Realized["missing"] = kisReplay ? [] : ["kis"];
    if (upbitTrades && !problems.some((p) => p.startsWith("업비트: "))) {
      const heldNow = new Map(upbit.holdings.map((h) => [h.symbol, h.quantity]));
      const replay = replayUpbit(upbitTrades.fills, heldNow, upbitTrades.closeAtStart);
      for (const h of upbit.holdings) {
        const since = replay.opened.get(h.symbol);
        // A coin with no fill since the start was held all along.
        h.since = since === undefined ? null : since || null;
      }
      sales.push(...replay.sales);
      problems.push(...replay.problems);
    } else missing.push("upbit");
    realized = summarize(sales, holdings, PRINCIPAL_SINCE, thisYear(), missing);
  }

  // The headline return, taken apart (see Bridge). "Other" is the remainder,
  // and the named parts of it are the causes that can be measured: dollars
  // held at KIS while the rate moved, USDT's own price, fees outside any
  // trade's cost. What is left of it is dividends, interest and the gap
  // between KIS's booked rate and the one an exchange actually got.
  let bridge: Bridge | null = null;
  if (principal && realized) {
    const unrealizedKrw = holdings.reduce((sum, h) => sum + h.valueKrw - h.costKrw, 0);
    const otherKrw = principal.profitKrw - realized.totalKrw - unrealizedKrw;
    const dollarCoinsKrw = upbit.cash.filter((c) => c.currency === "USD").reduce((sum, c) => sum + c.valueKrw, 0);
    const goldPart = principal.parts.find((p) => p.venue === "gold");
    const goldUnrealized = holdings.filter((h) => h.venue === "gold").reduce((sum, h) => sum + h.valueKrw - h.costKrw, 0);
    const named = [
      { id: "fx", label: "달러 환율(한투)", krw: kisFxKrw ?? 0 },
      { id: "usdt", label: "테더 시세", krw: "dollarCostKrw" in upbit ? dollarCoinsKrw - upbit.dollarCostKrw : 0 },
      {
        id: "fees",
        label: "출금·매수 수수료",
        krw: -(upbitFlowsRead?.feesKrw ?? 0) + (goldPart?.profitKrw != null ? goldPart.profitKrw - goldUnrealized : 0),
      },
    ];
    const rest = otherKrw - named.reduce((sum, n) => sum + n.krw, 0);
    bridge = {
      realizedKrw: realized.totalKrw,
      unrealizedKrw,
      otherKrw,
      other: [...named, { id: "rest", label: "배당·이자·환전 차이", krw: rest }].filter((n) => Math.abs(n.krw) >= 1),
    };
  }

  // The pot over time: each snapshot's total against what had gone in by its
  // day, and today live. Snapshots are taken on every build and, failing
  // that, once an hour by index.ts, so a day is missing only if the service
  // was down all of it.
  let track: TrackPoint[] = [];
  if (principal) {
    const flows = principal.flows;
    const putIn = (date: string) =>
      fixedKrw + flows.filter((f) => f.date <= date).reduce((sum, f) => sum + f.amountKrw, 0);
    const rows = await db.history(3650).catch(() => []);
    track = rows
      .filter((r) => r.date >= PRINCIPAL_SINCE && r.date < today)
      .map((r) => ({ date: r.date, totalKrw: r.totalKrw, principalKrw: putIn(r.date) }));
    track.push({ date: today, totalKrw: total, principalKrw: principal.principalKrw });
  }

  return {
    holdings,
    bridge,
    track,
    realized,
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
