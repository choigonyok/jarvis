// Server-side proxy to spending-svc. /api/spending/<path> → <SPENDING_URL>/<path>.
//
// The bearer token stays here, and this route sits behind the console login
// (see src/middleware.ts). The service's routes are few and all of them are
// the book's own - nothing here can reach the KakaoTalk collector behind it.
const SPENDING_URL = process.env.SPENDING_URL ?? "http://localhost:8095";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
): Promise<Response> {
  const { path } = await context.params;
  const search = new URL(request.url).search;
  const target = `${SPENDING_URL.replace(/\/$/, "")}/${path.map(encodeURIComponent).join("/")}${search}`;

  const headers = new Headers();
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  const withBody = request.method !== "GET" && request.method !== "DELETE";
  if (withBody) headers.set("content-type", "application/json");

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      body: withBody ? await request.text() : undefined,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json({ error: "가계부 서비스에 연결하지 못했습니다." }, { status: 502 });
  }

  const out = new Headers();
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  out.set("cache-control", "no-store");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const PUT = proxy;
export const DELETE = proxy;
