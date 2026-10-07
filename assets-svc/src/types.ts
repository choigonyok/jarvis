/**
 * The wire shape of the invest surface. Identical to what the web service
 * already sends, because the split and a contract change in one step would
 * leave two suspects when the page stops adding up.
 *
 * The browser-facing formatters (krw, percent, quantity) stay in the web
 * service - they are presentation, and nothing here renders anything.
 */

/** One shape for a coin and for a share. */
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
  /** Fraction: 0.031 is +3.1%. Null when nothing could be priced back then. */
  rate: number | null;
  amountKrw: number | null;
  /** Holdings with no price that far back. */
  missing: string[];
};

export type Changes = Record<Window, Change>;

/** Where money sits: KIS shares, Upbit coins, the KIS gold-spot account. */
export type Venue = "upbit" | "kis" | "gold" | "other";

/** One account measured against its own principal. */
export type PrincipalPart = {
  venue: Venue;
  /** What the account holds now, cash included. Null when it cannot be read. */
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
  /** "upbit:<uuid>" for what Upbit reported, "manual:<id>" for a recorded one. */
  id: string;
  date: string;
  venue: Venue;
  amountKrw: number;
  memo: string;
  /** Read from a brokerage, or written down by hand - only the latter can be deleted. */
  source: "auto" | "manual";
};

/**
 * Return measured against money actually put in since `since`, rather than
 * against what the current holdings cost - selling at a loss and buying
 * again resets cost, and money taken out disappears from it entirely.
 */
export type Principal = {
  since: string;
  principalKrw: number;
  profitKrw: number;
  rate: number;
  /** The same sum, split by account. */
  parts: PrincipalPart[];
  flows: Flow[];
};

/** The allocation buckets. Brokerages know coin and stock; the rest is ours - see target.ts. */
export type Bucket = "growth" | "coin" | "cash" | "dividend" | "gold";

export type Drift = {
  id: Bucket;
  label: string;
  valueKrw: number;
  current: number;
  target: number;
  /** Positive: buy this much. Negative: sell this much. */
  gapKrw: number;
  inBand: boolean;
  symbols: string[];
};

export type Move = {
  from: Bucket;
  to: Bucket;
  amountKrw: number;
  /** The bucket being filled is already inside its band - worth doing, not urgent. */
  optional: boolean;
};

/** Where the pot stands against its target allocation. Fixed assets are not in it. */
export type Allocation = {
  rows: Drift[];
  moves: Move[];
  totalKrw: number;
};

export type Portfolio = {
  holdings: Holding[];
  cashKrw: number;
  totalKrw: number;
  costKrw: number;
  profitKrw: number;
  returnRate: number;
  usdKrw: number;
  changes: Changes;
  series: Record<Window, Point[]>;
  at: string;
  /** Null when there is nothing to measure against yet. */
  principal: Principal | null;
  /** Counted in totalKrw and in principal; left out of holdings and cashKrw. */
  fixed: FixedAsset[];
  allocation: Allocation;
  /** A venue that failed is named rather than silently missing. */
  problems: string[];
};
