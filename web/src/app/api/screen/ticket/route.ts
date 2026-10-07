// A one-minute ticket for the 화면 tab's VNC socket.
//
// The socket goes to the gateway (jarvis-be), which wants JARVIS_API_TOKEN -
// but a browser cannot put a header on a WebSocket, and the token itself must
// never reach the browser. So this route, which sits behind the console login
// (src/middleware.ts), signs a short-lived ticket with the token instead. The
// gateway checks the signature and the expiry, and only for /vnc/websockify.
// It is only needed to open the socket; a session that is already open runs on.
const GATEWAY = process.env.JARVIS_VNC_GATEWAY ?? "";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";
const TTL_SECONDS = 60;

export const dynamic = "force-dynamic";

const hex = (buf: ArrayBuffer) =>
  [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");

export async function GET() {
  if (!GATEWAY || !API_TOKEN) {
    return Response.json({ error: "원격 화면이 설정되지 않았습니다." }, { status: 503 });
  }
  const exp = String(Math.floor(Date.now() / 1000) + TTL_SECONDS);
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(API_TOKEN),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sig = hex(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`vnc:${exp}`)));
  return Response.json(
    { url: `${GATEWAY}?ticket=${exp}.${sig}` },
    { headers: { "cache-control": "no-store" } },
  );
}
