import type { Holding } from "@/lib/portfolio";
import type { Bucket } from "@/lib/target";

/**
 * The shapes of a trade plan. The plan itself is worked out by assets-svc
 * (/plan, src/trade-plan.ts there), next to the prices it is built from:
 *
 * Turning "move 1,234,567원 into 배당주" into orders that can actually be
 * placed: a share is bought whole, KRX gold by the gram, a coin by amount
 * (with Upbit's minimum order). Whatever the rounding leaves over is cash -
 * the one bucket that takes any amount.
 *
 * Two questions, one calculation:
 * - rebalance: what to buy and sell so every bucket lands nearest its target.
 * - deposit: new cash arrives; what to buy with it (buying only) so the
 *   allocation moves toward target.
 *
 * Prices are the ones the brokerages last reported; fees and the FX spread
 * are left out.
 */

export type Order = {
  bucket: Bucket;
  /** The holding traded; null when the bucket holds nothing yet. */
  holding: Holding | null;
  side: "buy" | "sell";
  /** Whole shares / grams; for a coin, null (it trades by amount). */
  units: number | null;
  /** One unit's price in KRW; null for a coin. */
  unitKrw: number | null;
  amountKrw: number;
};

export type BucketResult = {
  id: Bucket;
  label: string;
  target: number;
  before: number;
  after: number;
  afterKrw: number;
};

export type Plan = {
  orders: Order[];
  /** Cash after the orders (and the deposit), in KRW. */
  cashAfterKrw: number;
  /** Change in cash; for a deposit, what stays uninvested. */
  cashChangeKrw: number;
  buckets: BucketResult[];
  /** Buckets with a gap but nothing in them to trade - the person picks. */
  unpicked: { bucket: Bucket; label: string; amountKrw: number }[];
};
