import { forward } from "@/lib/proxy";

// The couple's home as the adapter reads it: the two, since when, the blocks.
const CALENDAR_URL = (process.env.CALENDAR_URL ?? "http://localhost:8093").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export function GET(request: Request) {
  return forward(request, `${CALENDAR_URL}/couple`, { label: "우리", requestHeaders: [] });
}
