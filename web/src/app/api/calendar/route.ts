import { MIND_WRITES, forward } from "@/lib/proxy";

// A new entry a person filled in on the calendar's form: in at the way in,
// like a change to one (the [...path] route).
export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export function POST(request: Request) {
  return forward(request, `${MIND_WRITES}/api/calendar`, { label: "캘린더" });
}
