/**
 * The target allocation, as assets-svc computes it.
 *
 * The buckets, the symbol lists and the band live in assets-svc/src/target.ts,
 * so the console and the agent answer "how far off target" from one
 * calculation - two copies would disagree the first time a new dividend share
 * was added to only one of them. This file is only the wire shape.
 */
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
  /** How far `current` may sit from `target` and still be in band. Absent from an older assets-svc. */
  band?: number;
  /** Cash under its target: spent on the other buckets, and left out of the sums. */
  spent?: boolean;
  symbols: string[];
};

export type Move = {
  from: Bucket;
  to: Bucket;
  amountKrw: number;
  /** Both buckets are inside their bands - worth doing, not urgent. */
  optional: boolean;
};

/** Fixed assets (주택청약) are not in it - they cannot be moved. */
export type Allocation = {
  rows: Drift[];
  moves: Move[];
  totalKrw: number;
};
