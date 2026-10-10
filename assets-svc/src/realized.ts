import type { Holding, Realized, RealizedLine, Sale, Tax, TaxBasket } from "./types.js";
import { DOLLAR_COINS, type KisTrade, type UpbitFill } from "./venues.js";

/**
 * What has already been taken off the table.
 *
 * A holding's card shows what the units still held have made. Selling some
 * of them moves that profit out of the card and into cash - the total and the
 * return against principal do not change, but nothing said any more how much
 * that one holding had earned. And buying it again later blends the average,
 * so the card's own rate stops meaning "this is how it went".
 *
 * So sales are kept as their own record: each one with what it cost and what
 * it brought in, added up per holding, and per calendar year for the tax.
 */

/** Each taxed basket gets this much off its year's net gain. */
export const TAX_DEDUCTION_KRW = 2_500_000;
/** 20% income tax plus 2% local. */
export const TAX_RATE = 0.22;
/**
 * The first year virtual-asset gains are taxed. Postponed twice already (from
 * 2022, then from 2025); if it moves again, set COIN_TAX_FROM.
 */
export const COIN_TAX_FROM = Number(process.env.COIN_TAX_FROM ?? 2027);

const EPS = 1e-9;

/**
 * Upbit reports no profit per sale, so it is worked out the way Upbit keeps
 * its own average: a moving average of what was paid, unchanged by a sale.
 *
 * The replay starts at `from` with whatever was held then - today's quantity
 * with every fill since undone - priced at the close the day before, the same
 * way the principal ledger counts what was held on its first morning. Buy fees are part
 * of the cost; a sale's fee comes off its proceeds.
 *
 * A sale of more than the replay holds (a coin transferred in) has no known
 * cost for the excess; it is counted at the sale price - no gain - and named.
 */
export function replayUpbit(
  fills: UpbitFill[],
  heldNow: Map<string, number>,
  closeAtStart: Map<string, number>,
): { sales: Sale[]; problems: string[] } {
  const sales: Sale[] = [];
  const problems: string[] = [];
  const bySymbol = new Map<string, UpbitFill[]>();
  for (const f of fills) {
    if (DOLLAR_COINS.has(f.symbol)) continue; // dollars bought and sold are cash, not a bet
    bySymbol.set(f.symbol, [...(bySymbol.get(f.symbol) ?? []), f]);
  }

  for (const [symbol, list] of bySymbol) {
    const net = list.reduce((sum, f) => sum + (f.side === "bid" ? f.volume : -f.volume), 0);
    let qty = (heldNow.get(symbol) ?? 0) - net;
    if (qty < -EPS) {
      problems.push(`업비트 ${symbol}: 체결 내역과 지금 수량이 맞지 않아 일부 매도의 원가를 모릅니다.`);
      qty = 0;
    }
    qty = Math.max(0, qty);
    let cost = 0;
    if (qty > EPS) {
      const close = closeAtStart.get(symbol);
      if (close) cost = qty * close;
      else {
        problems.push(`업비트 ${symbol}: 기준일 종가가 없어 첫 체결가로 시작 원가를 잡았습니다.`);
        cost = qty * (list[0].fundsKrw / list[0].volume);
      }
    }

    for (const f of list) {
      if (f.side === "bid") {
        qty += f.volume;
        cost += f.fundsKrw + f.feeKrw;
        continue;
      }
      const covered = Math.min(f.volume, qty);
      const avg = qty > EPS ? cost / qty : 0;
      const excess = f.volume - covered;
      const soldCost = covered * avg + excess * (f.fundsKrw / f.volume);
      if (excess > EPS) {
        problems.push(`업비트 ${symbol}: ${f.date} 매도량이 보유량보다 많아 넘친 부분은 손익 0으로 셌습니다.`);
      }
      qty -= covered;
      cost -= covered * avg;
      if (qty < EPS) {
        qty = 0;
        cost = 0;
      }
      sales.push({
        id: `upbit:${f.uuid}`,
        date: f.date,
        venue: "upbit",
        kind: "coin",
        symbol,
        name: symbol,
        quantity: f.volume,
        proceedsKrw: f.fundsKrw,
        costKrw: soldCost,
        feeKrw: f.feeKrw,
        profitKrw: f.fundsKrw - f.feeKrw - soldCost,
      });
    }
  }
  return { sales, problems };
}

/**
 * KIS's own profit per sale (its period-profit report) converts the cost and
 * the proceeds both at the sale day's rate, so a won that strengthened while
 * the shares were held does not show: shares bought at 1,466 and sold at
 * 1,430 read far richer than they were - by about 40% on this account's
 * sales up to 2026-10-10.
 * The won actually paid and received is in every fill (KisTrade.netKrw), so
 * the sales are replayed from those instead, on a moving average in won - the
 * same footing a capital-gains return takes, each leg at its own day's rate.
 *
 * What is still held comes out of the same replay as its won cost
 * (`held`), so a holding's own return carries the currency too.
 *
 * Shares already held before `trades` begin have no known cost; a sale of
 * them is counted at its own price (no gain) and named.
 */
export function replayKis(
  trades: KisTrade[],
): { sales: Sale[]; held: Map<string, { quantity: number; costKrw: number }>; problems: string[] } {
  const sales: Sale[] = [];
  const problems: string[] = [];
  const book = new Map<string, { quantity: number; costKrw: number }>();
  for (const t of trades) {
    const pos = book.get(t.symbol) ?? { quantity: 0, costKrw: 0 };
    if (t.side === "buy") {
      pos.quantity += t.quantity;
      pos.costKrw += t.netKrw;
      book.set(t.symbol, pos);
      continue;
    }
    const covered = Math.min(t.quantity, pos.quantity);
    const avg = pos.quantity > EPS ? pos.costKrw / pos.quantity : 0;
    const excess = t.quantity - covered;
    const soldCost = covered * avg + excess * (t.netKrw / t.quantity);
    if (excess > EPS) {
      problems.push(`한국투자증권 ${t.name}: ${t.date} 매도분 일부는 조회 기간 전에 산 것이라 손익 0으로 셌습니다.`);
    }
    pos.quantity -= covered;
    pos.costKrw -= covered * avg;
    if (pos.quantity < EPS) {
      pos.quantity = 0;
      pos.costKrw = 0;
    }
    book.set(t.symbol, pos);
    sales.push({
      id: `kis:${t.date}:${t.symbol}:${sales.length}`,
      date: t.date,
      venue: "kis",
      kind: "stock",
      symbol: t.symbol,
      name: t.name,
      quantity: t.quantity,
      proceedsKrw: t.grossKrw,
      costKrw: soldCost,
      feeKrw: t.grossKrw - t.netKrw,
      profitKrw: t.netKrw - soldCost,
    });
  }
  return { sales, held: book, problems };
}

/** One basket's tax on a year's net gain. Losses in the year offset gains; nothing carries over. */
export function taxOn(gainKrw: number, kind: TaxBasket["kind"], year: number): TaxBasket {
  const inForce = kind === "overseas" || year >= COIN_TAX_FROM;
  const taxable = Math.max(0, gainKrw - TAX_DEDUCTION_KRW);
  return {
    kind,
    gainKrw,
    deductionKrw: TAX_DEDUCTION_KRW,
    rate: TAX_RATE,
    taxKrw: inForce ? Math.floor(taxable * TAX_RATE) : 0,
    inForce,
  };
}

/** The first day sales have to be read from: the principal's start, or January for this year's tax. */
export function salesFrom(since: string, year: number): string {
  const jan = `${year}-01-01`;
  return jan < since ? jan : since;
}

/**
 * Sales added up per holding since `since`, and this year's tax. `sales` may
 * reach further back than `since` (to January, for the tax); the per-holding
 * lines start at `since` like everything else measured against principal.
 */
export function summarize(
  sales: Sale[],
  holdings: Holding[],
  since: string,
  year: number,
  missing: Realized["missing"] = [],
): Realized {
  const sorted = [...sales].sort((a, b) => (a.date === b.date ? a.id.localeCompare(b.id) : b.date.localeCompare(a.date)));
  const inWindow = sorted.filter((s) => s.date >= since);
  const held = new Set(holdings.map((h) => h.id));

  const lines = new Map<string, RealizedLine>();
  for (const s of inWindow) {
    const id = `${s.venue}:${s.symbol}`;
    const line = lines.get(id) ?? {
      id,
      venue: s.venue,
      kind: s.kind,
      symbol: s.symbol,
      name: s.name,
      sales: 0,
      proceedsKrw: 0,
      costKrw: 0,
      profitKrw: 0,
      held: held.has(id),
    };
    line.sales += 1;
    line.proceedsKrw += s.proceedsKrw;
    line.costKrw += s.costKrw;
    line.profitKrw += s.profitKrw;
    lines.set(id, line);
  }

  const thisYear = sorted.filter((s) => s.date.startsWith(`${year}-`));
  const gain = (venue: Sale["venue"]) =>
    thisYear.filter((s) => s.venue === venue).reduce((sum, s) => sum + s.profitKrw, 0);
  const tax: Tax = {
    year,
    baskets: [taxOn(gain("kis"), "overseas", year), taxOn(gain("upbit"), "coin", year)],
  };

  const out = [...lines.values()].sort((a, b) => b.profitKrw - a.profitKrw);
  return {
    since,
    missing,
    sales: inWindow,
    lines: out,
    totalKrw: out.reduce((sum, l) => sum + l.profitKrw, 0),
    tax,
  };
}
