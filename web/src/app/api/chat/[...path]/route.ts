import { pathProxy } from "@/lib/proxy";

// What a person does in the conversation goes in at the way in: what they say
// and how they answer a card are records first, and the agent takes them from
// the queue, so one sent while the agent restarts waits for it, and a payment
// waiting on its card goes on whatever mind is doing. What comes back comes over
// the agent's stream. A card with a secret field does not come this way.
const INPUT_URL = (process.env.INPUT_URL ?? "http://localhost:8101").replace(/\/$/, "");
// A mark on an answer (좋아요·틀렸어요) is a record like the rest.
const allowed = /^(messages|cards\/[A-Za-z0-9_-]+\/decision|turns\/[a-z]+-[0-9]+-[0-9]+\/feedback)$/;

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const POST = pathProxy(`${INPUT_URL}/chat`, { label: "대화", allow: (p) => allowed.test(p) });
