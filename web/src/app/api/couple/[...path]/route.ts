import { MIND_WRITES, pathProxy } from "@/lib/proxy";

// The rest of the couple's space (uniple): read at its adapter, as it stands -
// a block, a habit's days, what the two did, the photos - and changed at the
// way in, like the calendar. The gateway lets through only these
// (calendarRoute, coupleWrite) and the hand only what it lists.
const CALENDAR_URL = (process.env.CALENDAR_URL ?? "http://localhost:8093").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const readable = (p: string) =>
  ["news", "photos", "cover"].includes(p) || /^blocks\/[^/]+(\/checks)?$/.test(p) || /^media\/[^/]+$/.test(p);

const writable = (p: string) => p === "blocks" || /^blocks\/[^/]+(\/(dday|bucket|free|checks|records)(\/[^/]+)?)?$/.test(p);

export const GET = pathProxy(`${CALENDAR_URL}/couple`, {
  label: "우리",
  requestHeaders: [],
  // A photo's size never changes under its id: the browser keeps it.
  responseHeaders: ["cache-control", "content-length"],
  allow: readable,
});

const write = pathProxy(`${MIND_WRITES}/api/couple`, { label: "우리", allow: writable });
export const POST = write;
export const PATCH = write;
export const DELETE = write;
