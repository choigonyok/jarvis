import { pathProxy } from "@/lib/proxy";

// context-svc, for what the console shows of it: the phone setup on the 상태
// screen, a day's way about on the 위치 screen, the guest's phone's own setup
// (위치's 내 폰 연결), and 위치 등록 - where one's own phone is now, made its
// region. The operator's setup carries the phone's credentials (OwnTracks'
// password, the shortcuts' signal token): only the operator reaches it - the
// guest reaches the track, the guest's own setup and the guest's own 위치 등록
// alone (lib/role.ts), and the gateway opens nothing else of context to the
// guest's token either. The rest of context-svc - the regions as a list, the
// state of the devices, the phones' mode and commands - is not reachable this
// way.
const CONTEXT_URL = process.env.CONTEXT_URL ?? "http://localhost:8103";
const allowed = new Set(["owntracks/setup", "owntracks/setup/guest", "track"]);
// 위치 등록: where the viewer's phone is now, made its region - the operator's
// own, or the guest's own (lib/role.ts and the gateway let the guest reach
// only theirs).
const posts = new Set(["owntracks/regions", "owntracks/regions/guest"]);

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(CONTEXT_URL, { label: "위치 서비스", requestHeaders: [], allow: (p) => allowed.has(p) });
export const POST = pathProxy(CONTEXT_URL, { label: "위치 서비스", requestHeaders: ["content-type"], allow: (p) => posts.has(p) });
