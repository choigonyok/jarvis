import type { Allocation } from "@/lib/target";

/** What the invest surface shows. One shape for a coin and for a share. */
export type Holding = {
  /** Stable id for React keys and dedupe: "upbit:BTC", "kis:AAPL". */
  id: string;
  venue: "upbit" | "kis" | "gold";
  kind: "coin" | "stock" | "gold";
  symbol: string;
  name: string;
  quantity: number;
  /** Per unit, in `currency`. */
  avgPrice: number;
  price: number;
  currency: "KRW" | "USD";
  /** Converted to KRW so one total can exist. */
  valueKrw: number;
  costKrw: number;
};

export type Window = "day" | "month" | "year";

export type Point = { date: string; valueKrw: number };

export type Change = {
  rate: number | null;
  amountKrw: number | null;
  missing: string[];
};

export const WINDOW_LABEL: Record<Window, string> = {
  day: "1일",
  month: "1개월",
  year: "1년",
};

export type Venue = "upbit" | "kis" | "gold" | "other";

/** One account measured against its own principal. */
export type PrincipalPart = {
  venue: Venue;
  /** Null when the account cannot be read yet. */
  valueKrw: number | null;
  principalKrw: number;
  profitKrw: number | null;
  rate: number | null;
};

/**
 * Money held outside any brokerage at a set amount - nothing to query, nothing
 * that moves. Part of the total, never part of the allocation.
 */
export type FixedAsset = { id: string; label: string; valueKrw: number };

/** One movement of money into (+) or out of (-) the pot. */
export type Flow = {
  id: string;
  date: string;
  venue: Venue;
  amountKrw: number;
  memo: string;
  /** Read from a brokerage, or written down by hand - only the latter can be deleted. */
  source: "auto" | "manual";
};

/** Return against money actually put in since `since`. */
export type Principal = {
  since: string;
  principalKrw: number;
  profitKrw: number;
  rate: number;
  parts: PrincipalPart[];
  flows: Flow[];
};

export const VENUE_LABEL: Record<Venue, string> = {
  upbit: "업비트",
  kis: "한국투자",
  gold: "금현물",
  other: "기타",
};

/** What each account is, in the words the headline uses. */
export const PART_LABEL: Record<Venue, string> = {
  kis: "주식",
  upbit: "코인",
  gold: "금",
  other: "기타",
};

export type Portfolio = {
  holdings: Holding[];
  /** Cash sitting at each venue, in KRW. */
  cashKrw: number;
  totalKrw: number;
  costKrw: number;
  /** totalKrw - costKrw, excluding cash on both sides. */
  profitKrw: number;
  /** As a fraction: 0.094 is +9.4%. */
  returnRate: number;
  usdKrw: number;
  /** The current basket re-priced at past closes - see assets-svc/src/change.ts. */
  changes: Record<Window, Change>;
  /** The same basket, day by day, for the curve. */
  series: Record<Window, Point[]>;
  at: string;
  /** Null when the ledger could not be read - the figure is withheld, not guessed. */
  principal: Principal | null;
  /** Counted in totalKrw and in principal; left out of holdings and cashKrw. */
  fixed: FixedAsset[];
  /** Target vs present, computed in assets-svc so the agent reads the same numbers. */
  allocation?: Allocation;
  /** A venue that failed is named rather than silently missing. */
  problems: string[];
};

export function profitOf(h: Holding) {
  return h.valueKrw - h.costKrw;
}

export function rateOf(h: Holding) {
  return h.costKrw > 0 ? (h.valueKrw - h.costKrw) / h.costKrw : 0;
}

/** ₩1,234,567 - no decimals; nobody reads won to the fraction. */
export function krw(value: number): string {
  return `₩${Math.round(value).toLocaleString("ko-KR")}`;
}

/** Signed, so a loss reads as a loss without relying on the colour. */
export function signedKrw(value: number): string {
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return `${sign}${krw(Math.abs(value))}`;
}

export function percent(rate: number, digits = 1): string {
  const sign = rate > 0 ? "+" : rate < 0 ? "−" : "";
  return `${sign}${(Math.abs(rate) * 100).toFixed(digits)}%`;
}

/** Quantities span 0.0026 BTC to 13 shares, so precision has to follow scale. */
export function quantity(value: number): string {
  if (value >= 1000) return value.toLocaleString("ko-KR", { maximumFractionDigits: 0 });
  if (value >= 1) return value.toLocaleString("ko-KR", { maximumFractionDigits: 4 });
  return value.toFixed(8).replace(/0+$/, "").replace(/\.$/, "");
}
