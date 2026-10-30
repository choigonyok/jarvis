import { MIND_URL, MIND_WRITES, pathProxy } from "@/lib/proxy";

// mind, for the 생활 screen and the 작업 tab: the notes it keeps about the
// operator, the ledgers it reads from their days, and each person's reminders
// and checklists. Written back: the operator's word on a note and a list's
// ticks and items, which go in at the way in. The rest of mind (the log, its
// clock, message text) is not reachable this way; the gateway refuses it too.
const reads = /^(notes|places|things|meals|reminders|checklists|notes\/[A-Za-z0-9_-]+\/versions)$/;
const writes = /^(notes\/[A-Za-z0-9_-]+|checklists\/[A-Za-z0-9_-]+(\/items\/[A-Za-z0-9_-]+)?)$/;

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(`${MIND_URL}/api`, { label: "생활 장부", requestHeaders: [], allow: (p) => reads.test(p) });
const write = pathProxy(`${MIND_WRITES}/api`, { label: "생활 장부", allow: (p) => writes.test(p) });

export const POST = write;
export const PATCH = write;
export const DELETE = write;
