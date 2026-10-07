// Server-side proxy to the KakaoTalk collector. The browser never reaches the
// collector directly: its bearer token and its read-only message surface stay
// behind this route, which itself sits behind the console login.
const KAKAOTALK_URL = process.env.KAKAOTALK_URL ?? "http://localhost:8090";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
) {
  const { path } = await context.params;
  const target = new URL(
    `${KAKAOTALK_URL.replace(/\/$/, "")}/${path.join("/")}`,
  );
  target.search = new URL(request.url).search;

  const headers = new Headers();
  const accept = request.headers.get("accept");
  if (accept) headers.set("accept", accept);
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: "GET",
      headers,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json(
      { error: "카카오톡 수집기에 연결하지 못했습니다." },
      { status: 502 },
    );
  }

  const out = new Headers();
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  out.set("cache-control", "no-store");

  return new Response(upstream.body, { status: upstream.status, headers: out });
}

// Read-only surface: GET only.
export const GET = proxy;
