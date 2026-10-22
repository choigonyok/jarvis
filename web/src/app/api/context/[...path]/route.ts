import { pathProxy } from "@/lib/proxy";

// context-svc, for what the console shows of it: the phone setup on the 상태
// screen, a day's way about on the 위치 screen, and the guest's phone's own
// setup (위치's 내 폰 연결). The operator's setup carries the phone's
// credentials (OwnTracks' password, the shortcuts' signal token): only the
// operator reaches it - the guest reaches the track and the guest's own setup
// alone (lib/role.ts), and the gateway opens nothing else of context to the
// guest's token either. The rest of context-svc - the state of the devices,
// regions, the phones' mode and commands - is not reachable this way.
const CONTEXT_URL = process.env.CONTEXT_URL ?? "http://localhost:8103";
const allowed = new Set(["owntracks/setup", "owntracks/setup/guest", "track"]);

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(CONTEXT_URL, { label: "위치 서비스", requestHeaders: [], allow: (p) => allowed.has(p) });
