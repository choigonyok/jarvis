import { ACTION } from "@/lib/kinds";
import type { CardChoice, Proposal } from "@/lib/thread";

/**
 * A card that asks a question with two answers rather than for an approval:
 * whether a job the operator asked for should go before the one on the
 * browser (agent internal/module/jobs KindFirst). Its 아니요 turns nothing
 * down - it is the other answer - so it asks no reason, and neither answer
 * is drawn in 반려's red.
 */
export function asksYesNo(p: Pick<Proposal, "action">): boolean {
  return p.action.kind === ACTION.jobFirst;
}

/**
 * How a card ended, as it reads. Not the state alone: a card nobody decided -
 * taken down because what it asked stopped mattering (its job's turn came, a
 * corrected card took its place) - reads rejected to every service, and was
 * drawn as the operator's 반려 in red; and a yes-or-no question's 아니요 is an
 * answer, not a turn-down.
 */
export type Outcome = "pending" | "approved" | "executed" | "rejected" | "failed" | "yes" | "no" | "closed";

export function outcomeOf(p: Pick<Proposal, "state" | "abandoned" | "action">): Outcome {
  if (p.state === "pending") return "pending";
  if (p.abandoned) return "closed";
  if (asksYesNo(p)) {
    if (p.state === "approved" || p.state === "executed") return "yes";
    if (p.state === "rejected") return "no";
  }
  return p.state;
}

/** The answer the operator chose among the card's own, when it offered its own. */
export function chosenOf(p: Pick<Proposal, "choice" | "card">): CardChoice | undefined {
  if (!p.choice) return undefined;
  return p.card.choices?.find((c) => c.id === p.choice);
}

/**
 * Whether a decided card's answer was a turn-down: 반려, or an answer of the
 * card's own drawn as one. An answer that is only the other answer (아니요, 순서대로)
 * is not, and says so in grey.
 */
export function turnedDown(p: Pick<Proposal, "state" | "abandoned" | "action" | "choice" | "card">): boolean {
  if (outcomeOf(p) !== "rejected") return false;
  const chosen = chosenOf(p);
  return !chosen || chosen.tone !== "neutral";
}
