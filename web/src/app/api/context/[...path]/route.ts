import { pathProxy } from "@/lib/proxy";

// context-svc, for the phone setup only. The setup carries the phone's
// credentials (OwnTracks' password, the shortcuts' signal token), so nothing
// else on context-svc is reachable this way.
const CONTEXT_URL = process.env.CONTEXT_URL ?? "http://localhost:8103";
const allowed = new Set(["owntracks/setup"]);

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(CONTEXT_URL, { label: "위치 서비스", requestHeaders: [], allow: (p) => allowed.has(p) });
