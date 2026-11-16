import { MIND_WRITES, pathProxy } from "@/lib/proxy";

// The rest of the couple's space: read at its adapter, as it stands -
// a block, a habit's days, what the two did, the photos - and changed at the
// way in, like the calendar. The gateway lets through only these
// (calendarRoute, coupleWrite) and the hand only what it lists.
const CALENDAR_URL = (process.env.CALENDAR_URL ?? "http://localhost:8093").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const readable = (p: string) =>
  ["news", "photos", "cover", "archived"].includes(p) || /^blocks\/[^/]+(\/checks)?$/.test(p) || /^media\/[^/]+$/.test(p);

const writable = (p: string) =>
  ["blocks", "order", "photos"].includes(p) ||
  /^blocks\/[^/]+(\/(dday|bucket|free|checks|records|visits|start|restore)(\/[^/]+)?)?$/.test(p) ||
  /^photos\/[^/]+(\/cover)?$/.test(p) ||
  /^blocks\/[^/]+\/album(\/(delete|pin))?$/.test(p);

export const GET = pathProxy(`${CALENDAR_URL}/couple`, {
  label: "우리",
  requestHeaders: [],
  // A photo's size never changes under its id: the browser keeps it.
  responseHeaders: ["cache-control", "content-length"],
  allow: readable,
});

const write = pathProxy(`${MIND_WRITES}/api/couple`, { label: "우리", allow: writable });

// A photo's bytes go straight to the adapter, which hands back the asset to
// put somewhere - that putting is a change like any other, at the way in. So
// does how far one has read the news: it is theirs alone and changes nothing.
const direct = (p: string) => p === "uploads" || p === "news/seen";
const straight = pathProxy(`${CALENDAR_URL}/couple`, { label: "우리", allow: direct });

export function POST(request: Request, context: { params: Promise<{ path: string[] }> }) {
  return context.params.then(({ path }) => (direct(path.join("/")) ? straight : write)(request, context));
}
export const PATCH = write;
export const DELETE = write;
