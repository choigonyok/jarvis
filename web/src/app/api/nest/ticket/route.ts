// A one-minute ticket into 둥지 (dungji/), the room the two keep together.
//
// 둥지 is its own service - other couples sign up there too - so it knows
// nothing of this console's login. This route, behind that login
// (src/middleware.ts), signs who is here - the operator or the guest, the
// role the middleware stamped - with DUNGJI_SSO_SECRET; 둥지 checks the
// signature and the expiry, takes each ticket once, and lets that person in
// as the 둥지 account tied to their seat (dungji/worker/index.ts, /api/sso/jarvis).
// The two seats share one home there: whoever comes first builds it.
import { ROLE_HEADER } from "@/lib/role";

const DUNGJI_URL = (process.env.DUNGJI_URL ?? "").replace(/\/$/, "");
const SECRET = process.env.DUNGJI_SSO_SECRET ?? "";
const TTL_SECONDS = 60;

export const dynamic = "force-dynamic";

const hex = (bytes: ArrayBuffer | Uint8Array) =>
  [...new Uint8Array(bytes)].map((b) => b.toString(16).padStart(2, "0")).join("");

export async function GET(request: Request) {
  if (!DUNGJI_URL || !SECRET) {
    return Response.json({ error: "둥지가 아직 설정되지 않았어요." }, { status: 503 });
  }
  const who = request.headers.get(ROLE_HEADER) === "guest" ? "guest" : "owner";
  const exp = Math.floor(Date.now() / 1000) + TTL_SECONDS;
  const nonce = hex(crypto.getRandomValues(new Uint8Array(16)));
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(SECRET),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sig = hex(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`dungji:${who}:${exp}:${nonce}`)));
  return Response.json(
    { url: `${DUNGJI_URL}/api/sso/jarvis?ticket=v1.${who}.${exp}.${nonce}.${sig}` },
    { headers: { "cache-control": "no-store" } },
  );
}
