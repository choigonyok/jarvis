import { pathProxy } from "@/lib/proxy";

// context-svc, for what the console shows of it: the phone setup on the 상태
// screen, and a day's way about on the 위치 screen. The setup carries the
// phone's credentials (OwnTracks' password, the shortcuts' signal token), and
// only the operator reaches /api/context (lib/role.ts). The rest of
// context-svc - the state of the devices, regions, the phone's mode and
// commands - is not reachable this way.
const CONTEXT_URL = process.env.CONTEXT_URL ?? "http://localhost:8103";
const allowed = new Set(["owntracks/setup", "track"]);

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(CONTEXT_URL, { label: "위치 서비스", requestHeaders: [], allow: (p) => allowed.has(p) });
