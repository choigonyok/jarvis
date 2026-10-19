import { pathProxy } from "@/lib/proxy";

// mind, for the 생활 screen: the notes it keeps about the operator and the
// ledgers it reads from their days - and the one thing written back, the
// operator's word on a note. The rest of mind (the log, its clock, message
// text) is not reachable this way; the gateway refuses it too.
const MIND_URL = process.env.MIND_URL ?? "http://localhost:8102";
const base = `${MIND_URL.replace(/\/$/, "")}/api`;

const reads = /^(notes|places|things|meals|notes\/[A-Za-z0-9_-]+\/versions)$/;
const writes = /^notes\/[A-Za-z0-9_-]+$/;

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(base, { label: "생활 장부", requestHeaders: [], allow: (p) => reads.test(p) });
export const POST = pathProxy(base, { label: "생활 장부", allow: (p) => writes.test(p) });
