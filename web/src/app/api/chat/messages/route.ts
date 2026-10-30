import { forward } from "@/lib/proxy";

// What a person says in the conversation goes in at the way in: it is a record
// first, and the agent takes it from the queue, so one sent while the agent
// restarts waits for it. What comes back comes over the agent's stream.
const INPUT_URL = (process.env.INPUT_URL ?? "http://localhost:8101").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  return forward(request, `${INPUT_URL}/chat/messages`, { label: "대화" });
}
