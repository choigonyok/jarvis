"use client";

import { useEffect, useState } from "react";
import { keepValue } from "@/lib/keep";
import { API, type CardChoice, type Decision, type Proposal } from "@/lib/thread";

/**
 * What became of a decision posted: taken, or why not. `already`: the card had
 * been decided before this one reached it - on another screen, a moment ago -
 * so there is nothing to say in red, only the card to read again.
 */
export type Posted = { ok: true; already?: boolean } | { ok: false; error: string };

/**
 * Decisions this page sent that the card has not come back with yet, by card.
 * A card that has one says so - "승인을 보냈어요 · 반영 중" - instead of
 * offering its buttons again: the card stays 결재 대기 until the stream
 * brings it, and a second press would meet "이미 결재된 요청" in red on a
 * card about to read 승인함. Kept for as long as the page is open, so a card
 * drawn again (another tab and back) still says it.
 */
const sent = new Map<string, Decision>();

/** The answer chosen on a card and not yet back, by its label, when the card offered its own answers. */
const chosen = new Map<string, string>();

/** The decision sent on a card and not yet back, if one was. */
export function sentOn(proposalId: string): Decision | undefined {
  return sent.get(proposalId);
}

/** The label of the answer sent on a card and not yet back, if it offered its own. */
export function sentChoice(proposalId: string): string | undefined {
  return chosen.get(proposalId);
}

/**
 * Where what is being written on a waiting card is kept while the app is
 * open (lib/keep.ts), by the card: its fields, and a turn-down's reason - the
 * answer that asked for it, and the words. A card drawn again (another tab
 * and back, a send refused) has them still; back decided, it lets them go.
 */
export function typedOn(proposalId: string, what: "values" | "asking" | "why"): string {
  return `card:${proposalId}:${what}`;
}

/** The card came back decided: it says so itself now, and nothing typed on it is kept. */
export function heardBack(proposalId: string) {
  sent.delete(proposalId);
  chosen.delete(proposalId);
  for (const what of ["values", "asking", "why"] as const) keepValue(typedOn(proposalId, what), undefined);
}

/**
 * A waiting card's decision, from the press until the card comes back with
 * it: on its way, then taken and not back yet - said where its buttons were,
 * never the buttons again while it is - or refused, and why, to be said where
 * it was pressed. Every place a card is decided (the conversation, 기록) keeps
 * it this one way. `post` resolves to why it was refused.
 */
export function useDecision(
  proposal: Pick<Proposal, "id" | "state">,
  post: (decision: Decision, reason?: string, choice?: Pick<CardChoice, "id" | "label">) => Promise<string | null> | void,
) {
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const [sentAs, setSentAs] = useState(() => sentOn(proposal.id));
  const decided = proposal.state !== "pending";
  useEffect(() => {
    if (decided) heardBack(proposal.id);
  }, [decided, proposal.id]);

  /** Resolves to whether it was taken. */
  async function answer(c: CardChoice, reason?: string): Promise<boolean> {
    setRefusal(null);
    setBusy(true);
    // An answer of the card's own goes back with its id; 반려 and 승인 stand for none.
    const refused = await post(c.decision, reason, c.id ? { id: c.id, label: c.label } : undefined);
    setBusy(false);
    if (refused) {
      setRefusal(refused);
      return false;
    }
    setSentAs(sentOn(proposal.id));
    return true;
  }

  return { busy, refusal, sent: decided ? undefined : sentAs, answer };
}

/** How long a card may take to come back decided over the stream before it is read again. */
export const DECIDE_ECHO_MS = 4000;

/**
 * Posts one decision. Shared by the thread and the calendar because approving
 * is the same act wherever the card is shown.
 */
export async function decideProposal(
  proposalId: string,
  decision: Decision,
  /** A form card's fields as the operator left them. */
  edits?: Record<string, string>,
  /** Why, when the operator says - a rejection's reason. */
  reason?: string,
  /** The card has a secret field: a secret is a value, not a record, so its answer goes straight to the agent. */
  secret?: boolean,
  /** The answer chosen among the card's own: it, not the decision, says what is decided. */
  choice?: Pick<CardChoice, "id" | "label">,
): Promise<Posted> {
  try {
    const res = await fetch(secret ? `${API}/proposals/${proposalId}/decision` : `/api/chat/cards/${encodeURIComponent(proposalId)}/decision`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        decision,
        ...(edits ? { edits } : {}),
        ...(reason?.trim() ? { reason: reason.trim() } : {}),
        ...(choice ? { choice: choice.id } : {}),
      }),
      // Sent as the page goes away too: an answer given is an answer kept.
      keepalive: true,
    });
    // 202 too: the answer is kept and will be done; the card says it is on its way.
    if (res.ok) {
      sent.set(proposalId, decision);
      if (choice) chosen.set(proposalId, choice.label);
      return { ok: true };
    }
    // Decided already (agent: 이미 결재된 요청입니다) - read the card again.
    if (res.status === 409) return { ok: true, already: true };
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    return { ok: false, error: body?.error ?? "결재를 반영하지 못했어요." };
  } catch {
    return { ok: false, error: "에이전트에 연결하지 못했어요." };
  }
}
