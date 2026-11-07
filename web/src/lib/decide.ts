import { API, type Decision } from "@/lib/thread";

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

/** The decision sent on a card and not yet back, if one was. */
export function sentOn(proposalId: string): Decision | undefined {
  return sent.get(proposalId);
}

/** The card came back decided: it says so itself now. */
export function heardBack(proposalId: string) {
  sent.delete(proposalId);
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
): Promise<Posted> {
  try {
    const res = await fetch(secret ? `${API}/proposals/${proposalId}/decision` : `/api/chat/cards/${encodeURIComponent(proposalId)}/decision`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ decision, ...(edits ? { edits } : {}), ...(reason?.trim() ? { reason: reason.trim() } : {}) }),
      // Sent as the page goes away too: an answer given is an answer kept.
      keepalive: true,
    });
    // 202 too: the answer is kept and will be done; the card says it is on its way.
    if (res.ok) {
      sent.set(proposalId, decision);
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
