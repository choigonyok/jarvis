// Server-side proxy to context-svc, for the phone setup only.
// /api/context/owntracks/setup → <CONTEXT_URL>/owntracks/setup.
//
// The setup carries the phone's credentials (OwnTracks' password, the
// shortcuts' signal token), so it is read here, behind the console login,
// and never cached. Nothing else on context-svc is reachable this way.
const CONTEXT_URL = process.env.CONTEXT_URL ?? "http://localhost:8103";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const allowed = new Set(["owntracks/setup"]);

export async function GET(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
): Promise<Response> {
  const { path } = await context.params;
  const joined = path.join("/");
  if (!allowed.has(joined)) {
    return Response.json({ error: "없는 경로입니다." }, { status: 404 });
  }
  const headers = new Headers();
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  let upstream: Response;
  try {
    upstream = await fetch(`${CONTEXT_URL.replace(/\/$/, "")}/${joined}`, {
      headers,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json({ error: "위치 서비스에 연결하지 못했습니다." }, { status: 502 });
  }
  const out = new Headers({ "cache-control": "no-store" });
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  return new Response(upstream.body, { status: upstream.status, headers: out });
}
