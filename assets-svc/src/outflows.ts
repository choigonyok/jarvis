import { FLOW_MIN_KRW } from "./kis-flows.js";
import type { Flow, FlowRef, Outflow, OutflowAnswer, OutflowPart } from "./types.js";

/**
 * Where each withdrawal from the pot went.
 *
 * Money taken out of one account here is often put into another - Upbit to
 * the bank to KIS, KIS to the gold account - and then it is moved, not gone.
 * What does not come back was spent, kept somewhere else, or is still in the
 * bank; nothing here can see a bank, so that part is asked about (the agent's
 * card, agent/internal/module/assets/ask.go) and the answer is kept
 * (outflow_uses). What was spent goes into the 가계부 from there, in the record
 * this service leaves when the list changes (assets.outflows).
 *
 * None of it changes a number on the assets screen: a withdrawal left the
 * principal whatever it was for. The use is a label on it.
 *
 * Found transfers are worked out on every look, from the flows as they are
 * then - a hand-kept row deleted, or a found KIS row recomputed, changes the
 * pairs with it. Only what a person said is stored.
 *
 * A pair is found only where it is plain: the same money (within the larger
 * of ₩5,000 and half a percent) into an account here within two days, or a
 * little more - topped up from the bank on the way. A wrong pair would make a
 * spend vanish without a word; a question costs one card, and there are a few
 * a month.
 */

/** A deposit this many days after a withdrawal may still be the same money moved. */
export const MATCH_DAYS = 2;
/** A withdrawal nothing explains is asked about once it is this many days old. */
export const ASK_AFTER_DAYS = 3;
/** Money said to be still in the bank is looked for this long, then asked about again. */
export const PARKED_DAYS = 30;
/** How far after a withdrawal a deposit is offered on the card as what it may have become. */
const CANDIDATE_DAYS = 14;
/** A found KIS row is dated the day it was seen, which can be days after the money moved. */
const FOUND_LAG_DAYS = 3;
/** On its way to another account, money may be topped up from the bank by this much. */
const TOP_UP = 0.05;

/** Close enough to be the same money: a fee, rounding, an exchange's spread. */
export const tolerance = (krw: number) => Math.max(FLOW_MIN_KRW, krw * 0.005);

/**
 * A flow's identity across looks. A found KIS or gold row is rewritten on
 * every look and replaced when it cancels out, so its row id is not who it
 * is - its account and day are.
 */
export function flowKey(f: Flow): string {
  if (f.source === "auto" && f.id.startsWith("manual:")) return `auto:${f.venue}:${f.date}`;
  return f.id;
}

const DAY_MS = 86_400_000;
const dayNumber = (date: string) => Date.parse(`${date}T00:00:00Z`) / DAY_MS;
const addDays = (date: string, days: number) => new Date((dayNumber(date) + days) * DAY_MS).toISOString().slice(0, 10);
const seoulDay = (ms: number) => new Date(ms).toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
const sum = (parts: { amountKrw: number }[]) => parts.reduce((total, p) => total + p.amountKrw, 0);

/** Whether `deposit` can be `amount` moved: the same money, or a little more. */
const carries = (deposit: number, amount: number) =>
  deposit >= amount - tolerance(amount) && deposit <= amount * (1 + TOP_UP) + tolerance(amount);

type Out = {
  flow: Flow;
  key: string;
  amountKrw: number;
  /** The earliest day the money can have moved: a found row may be dated late. */
  from: string;
  /** What a person said, about this amount - an answer about another is stale. */
  answers: OutflowAnswer[];
  found: OutflowPart[];
};

export function outflowsOf(flows: Flow[], answers: OutflowAnswer[], today: string, now = Date.now()): Outflow[] {
  const outs: Out[] = [];
  const ins: FlowRef[] = [];
  for (const f of flows) {
    const amount = Math.round(Math.abs(f.amountKrw));
    if (amount < FLOW_MIN_KRW) continue;
    const key = flowKey(f);
    if (f.amountKrw > 0) {
      ins.push({ key, date: f.date, venue: f.venue, amountKrw: amount });
    } else {
      outs.push({
        flow: f,
        key,
        amountKrw: amount,
        from: key.startsWith("auto:") ? addDays(f.date, -FOUND_LAG_DAYS) : f.date,
        answers: answers.filter((a) => a.key === key && Math.abs(a.flowKrw - amount) < 1),
        found: [],
      });
    }
  }
  ins.sort((a, b) => a.date.localeCompare(b.date));

  const used = new Set<string>();
  // What is left to account for: a pair is looked for only for that.
  const open = (o: Out) => o.amountKrw - sum(o.answers) - sum(o.found);
  const near = (o: Out, i: FlowRef) => !used.has(i.key) && i.date >= o.from && i.date <= addDays(o.flow.date, MATCH_DAYS);
  // Oldest first, and of one day the bigger first.
  const order = [...outs].sort((a, b) => a.flow.date.localeCompare(b.flow.date) || b.amountKrw - a.amountKrw);

  // Whole first, for every withdrawal before any is split: a deposit that is
  // one withdrawal's whole should not go to fill part of a bigger one.
  for (const o of order) {
    const need = open(o);
    if (need < tolerance(o.amountKrw)) continue;
    const pair = ins
      .filter((i) => near(o, i) && carries(i.amountKrw, need))
      .sort((a, b) => Math.abs(a.amountKrw - need) - Math.abs(b.amountKrw - need) || a.date.localeCompare(b.date))[0];
    if (!pair) continue;
    used.add(pair.key);
    o.found.push({ use: "transfer", amountKrw: need, to: pair });
  }
  // Then smaller deposits soon after, biggest first, as far as they go. What
  // is left is asked about, with them named on the card.
  for (const o of order) {
    for (const i of ins.filter((x) => near(o, x)).sort((a, b) => b.amountKrw - a.amountKrw)) {
      if (open(o) < tolerance(o.amountKrw)) break;
      if (i.amountKrw > open(o) + tolerance(o.amountKrw)) continue;
      used.add(i.key);
      o.found.push({ use: "transfer", amountKrw: i.amountKrw, to: i });
    }
  }

  const result = outs.map((o): Outflow & { from: string } => {
    const parts: OutflowPart[] = [...o.found];
    let said = 0;
    for (const a of o.answers) {
      const part: OutflowPart = { use: a.use, amountKrw: a.amountKrw, decidedAt: a.decidedAt };
      if (a.category) part.category = a.category;
      if (a.memo) part.memo = a.memo;
      if (a.use === "parked") {
        // Parked money that came back into an account here was moved after
        // all; parked too long without coming back is asked about again.
        const until = seoulDay(Date.parse(a.decidedAt) + PARKED_DAYS * DAY_MS);
        const back = ins.find((i) => !used.has(i.key) && i.date >= o.flow.date && i.date <= until && carries(i.amountKrw, a.amountKrw));
        if (back) {
          used.add(back.key);
          part.use = "transfer";
          part.to = back;
        } else if (now - Date.parse(a.decidedAt) > PARKED_DAYS * DAY_MS) {
          continue;
        }
      }
      parts.push(part);
      said++;
    }
    const rest = o.amountKrw - sum(parts);
    const askKrw = rest < tolerance(o.amountKrw) ? 0 : Math.round(rest);
    let status: Outflow["status"] = "ask";
    if (askKrw === 0) status = said > 0 ? "answered" : "transfer";
    else if (dayNumber(today) - dayNumber(o.flow.date) < ASK_AFTER_DAYS) status = "waiting";
    return {
      key: o.key,
      date: o.flow.date,
      venue: o.flow.venue,
      amountKrw: o.amountKrw,
      memo: o.flow.memo,
      status,
      parts,
      askKrw,
      candidates: [],
      from: o.from,
    };
  });

  // Once every pair is taken: deposits soon after that nothing claimed.
  return result
    .sort((a, b) => a.date.localeCompare(b.date) || a.key.localeCompare(b.key))
    .map(({ from, ...o }) => {
      if (o.askKrw > 0) {
        o.candidates = ins
          .filter((i) => !used.has(i.key) && i.date >= from && i.date <= addDays(o.date, CANDIDATE_DAYS))
          .slice(0, 3);
      }
      return o;
    });
}
