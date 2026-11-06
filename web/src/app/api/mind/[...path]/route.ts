import { MIND_URL, MIND_WRITES, pathProxy } from "@/lib/proxy";

// mind, for the 생활 screen and the 작업·목표 tabs: the notes it keeps about the
// operator, the ledgers it reads from their days, and each person's reminders,
// checklists and goals. Written back: the operator's word on a note, a list's
// ticks and items, and a goal and its days, which go in at the way in. The rest
// of mind (the log, its clock, message text) is not reachable this way; the
// gateway refuses it too.
// attention: when the operator answers cards, which the notification settings
// offer the digest's time by. intents/{id}/useful|useless: the inbox's one tap
// on a notice mind sent.
const reads = /^(notes|places|things|meals|reminders|checklists|attention|goals(\/[A-Za-z0-9_-]+)?|notes\/[A-Za-z0-9_-]+\/versions)$/;
const writes = /^(notes\/[A-Za-z0-9_-]+|intents\/[A-Za-z0-9_-]+\/(useful|useless)|checklists\/[A-Za-z0-9_-]+(\/items\/[A-Za-z0-9_-]+)?|goals(\/[A-Za-z0-9_-]+(\/checks)?)?)$/;

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(`${MIND_URL}/api`, { label: "생활 장부", requestHeaders: [], allow: (p) => reads.test(p) });
const write = pathProxy(`${MIND_WRITES}/api`, { label: "생활 장부", allow: (p) => writes.test(p) });

export const POST = write;
export const PATCH = write;
export const DELETE = write;
