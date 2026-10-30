import { MIND_WRITES, pathProxy } from "@/lib/proxy";

// The couple's calendar: read at its adapter, as it stands, and changed at the
// way in (an entry edited or taken off; a new one comes through the conversation
// and its card). The gateway lets through only these (calendarRoute, mindRoute).
const CALENDAR_URL = (process.env.CALENDAR_URL ?? "http://localhost:8093").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const write = pathProxy(`${MIND_WRITES}/api/calendar`, { label: "캘린더", allow: (p) => p !== "events" && !p.includes("/") });

export const GET = pathProxy(CALENDAR_URL, { label: "캘린더", requestHeaders: [], allow: (p) => p === "events" });
export const PATCH = write;
export const DELETE = write;
