// Server-side proxy to status-svc. /api/status/<path> → <STATUS_URL>/<path>.
//
// Read-only apart from /refresh, which only makes the service look again.
// The bearer token stays here, behind the console login.
const STATUS_URL = process.env.STATUS_URL ?? "http://localhost:8096";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
): Promise<Response> {
  const { path } = await context.params;
  const target = `${STATUS_URL.replace(/\/$/, "")}/${path.map(encodeURIComponent).join("/")}`;

  const headers = new Headers();
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json({ error: "상태 서비스에 연결하지 못했습니다." }, { status: 502 });
  }

  const out = new Headers();
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  out.set("cache-control", "no-store");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
