import { API, type Decision } from "@/lib/thread";

/**
 * Posts one decision. Shared by the thread and the calendar because approving
 * is the same act wherever the card is shown.
 *
 * Returns null on success, or the message to put in front of the operator.
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
): Promise<string | null> {
  try {
    const res = await fetch(secret ? `${API}/proposals/${proposalId}/decision` : `/api/chat/cards/${encodeURIComponent(proposalId)}/decision`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ decision, ...(edits ? { edits } : {}), ...(reason?.trim() ? { reason: reason.trim() } : {}) }),
    });
    if (res.ok) return null;
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    return body?.error ?? "결재를 반영하지 못했습니다.";
  } catch {
    return "에이전트에 연결하지 못했습니다.";
  }
}
