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

/**
 * Cash, by account and currency. cashKrw is their sum; these say where it
 * sits and in what. Upbit's USDT is here, as dollars - a stablecoin held to
 * be dollars is cash, not a coin bet.
 */
export type CashLine = {
  id: string;
  venue: "upbit" | "kis" | "gold";
  currency: "KRW" | "USD";
  label: string;
  /** In `currency`. */
  amount: number;
  valueKrw: number;
};

/** One holding's price return since the start of a window, day by day. */
export type ReturnPoint = { date: string; rate: number };

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
/**
 * A place the hand-kept ledger disagrees with what the brokerage's trades
 * say (see kis-flows.ts). "short": the books went negative on `date`;
 * "total": the ledger does not end at today's cash, off by `amountKrw`.
 */
export type LedgerCheck = {
  venue: Venue;
  kind: "short" | "total";
  date?: string;
  amountKrw: number;
  message: string;
};

export type Principal = {
  since: string;
  principalKrw: number;
  profitKrw: number;
  rate: number;
  /** The same sum, split by account. */
  parts: PrincipalPart[];
  flows: Flow[];
  /** Empty when the ledger agrees with the trades, or they could not be read. */
  checks: LedgerCheck[];
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

/**
 * One sale and what it made. KIS reports this itself; Upbit does not, so its
 * sales are worked out by replaying the trades (see realized.ts).
 */
export type Sale = {
  /** "kis:20261006:SPCX:0", "upbit:<order uuid>". */
  id: string;
  date: string;
  venue: "kis" | "upbit";
  kind: "stock" | "coin";
  symbol: string;
  name: string;
  quantity: number;
  /** What the sale brought in, before fees, in won. */
  proceedsKrw: number;
  /** What the units sold had cost (moving average), in won. */
  costKrw: number;
  feeKrw: number;
  /** proceeds - cost - fee. */
  profitKrw: number;
};

/** Everything sold of one holding since `since`, added up. */
export type RealizedLine = {
  /** Same id as the Holding it belongs to: "kis:SPCX", "upbit:BTC". */
  id: string;
  venue: "kis" | "upbit";
  kind: "stock" | "coin";
  symbol: string;
  name: string;
  sales: number;
  proceedsKrw: number;
  costKrw: number;
  profitKrw: number;
  /** Still held. False: sold out - the line is all that is left of it. */
  held: boolean;
};

/**
 * Capital-gains tax, estimated for one calendar year. Overseas shares and
 * (from 2027) virtual assets are taxed separately, each with its own 2.5M
 * deduction, at 22% local tax included. Domestic minority shares and KRX gold
 * are not taxed and are not here.
 */
export type TaxBasket = {
  kind: "overseas" | "coin";
  /** Net gain realized so far this year (losses offset gains), in won. */
  gainKrw: number;
  deductionKrw: number;
  rate: number;
  taxKrw: number;
  /** False while the tax is not in force (virtual assets before 2027). */
  inForce: boolean;
};

export type Tax = { year: number; baskets: TaxBasket[] };

export type Realized = {
  since: string;
  /** A venue whose sales could not be read: its lines and tax are missing, not zero. */
  missing: ("kis" | "upbit")[];
  sales: Sale[];
  lines: RealizedLine[];
  totalKrw: number;
  tax: Tax;
};

/**
 * One recorded day of the whole pot: what it was worth and how much had been
 * put in by then. Their gap is the return; the rate is that over principal.
 */
export type TrackPoint = { date: string; totalKrw: number; principalKrw: number };

export type Portfolio = {
  holdings: Holding[];
  /**
   * The pot day by day, from the daily snapshots, with today's live numbers
   * as the last point. Empty when the principal or the snapshots cannot be read.
   */
  track: TrackPoint[];
  /** Null when neither brokerage's sales could be read. */
  realized: Realized | null;
  cashKrw: number;
  cash: CashLine[];
  /** KRX gold, won per gram - so a plan can buy gold before any is held. Null without a gold account. */
  goldGramKrw: number | null;
  /** Per holding id: its price return over each window. */
  holdingSeries: Record<string, Record<Window, ReturnPoint[]>>;
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
