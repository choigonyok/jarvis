// Server-side proxy to notify-svc, through the gateway. /api/notify/<path> →
// <NOTIFY_URL>/<path>. The owner's only (lib/role.ts lets a guest nowhere near it).
const NOTIFY_URL = process.env.NOTIFY_URL ?? "http://localhost:8097";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(request: Request, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  const target = `${NOTIFY_URL.replace(/\/$/, "")}/${path.map(encodeURIComponent).join("/")}${new URL(request.url).search}`;
  const headers = new Headers({ "content-type": "application/json" });
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  const ua = request.headers.get("x-client-user-agent") ?? request.headers.get("user-agent");
  if (ua) headers.set("x-client-user-agent", ua);
  try {
    const upstream = await fetch(target, {
      method: request.method,
      headers,
      body: request.method === "GET" || request.method === "HEAD" ? undefined : await request.text(),
      cache: "no-store",
    });
    return new Response(upstream.body, {
      status: upstream.status,
      headers: { "content-type": upstream.headers.get("content-type") ?? "application/json", "cache-control": "no-store" },
    });
  } catch {
    return Response.json({ error: "알림 서비스에 연결하지 못했습니다." }, { status: 502 });
  }
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
