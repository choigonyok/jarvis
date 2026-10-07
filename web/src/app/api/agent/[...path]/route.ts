import { ROLE_HEADER } from "@/lib/role";

// The browser never talks to the agent directly: the API key and the agent's
// surface stay on the server side of this proxy.
const AGENT_URL = process.env.AGENT_URL ?? "http://localhost:8080";
// Once the console is served from Cloudflare, the agent is reachable at a
// public hostname of its own. This is what stops that hostname from being a
// way around the login: the agent refuses anything without it.
const AGENT_TOKEN = process.env.JARVIS_API_TOKEN ?? "";
// The guest account talks to its own agent: its own conversation, calendar
// only. The middleware decides who is a guest and stamps the request.
const AGENT_GUEST_URL = process.env.AGENT_GUEST_URL ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
) {
  const { path } = await context.params;
  const guest = request.headers.get(ROLE_HEADER) === "guest";
  if (guest && !AGENT_GUEST_URL) {
    return Response.json({ error: "손님용 jarvis 가 설정되지 않았습니다." }, { status: 503 });
  }
  const base = guest ? AGENT_GUEST_URL : AGENT_URL;
  const target = new URL(
    `${base.replace(/\/$/, "")}/${path.join("/")}`,
  );
  target.search = new URL(request.url).search;

  const headers = new Headers();
  const contentType = request.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);
  const accept = request.headers.get("accept");
  if (accept) headers.set("accept", accept);
  if (AGENT_TOKEN) headers.set("authorization", `Bearer ${AGENT_TOKEN}`);

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      // Bytes, not text: a photo upload passes through here too.
      body: request.method === "GET" ? undefined : await request.arrayBuffer(),
      cache: "no-store",
      // The event stream must outlive the request that opened it.
      signal: request.signal,
    });
  } catch {
    return Response.json(
      { error: "에이전트에 연결하지 못했습니다." },
      { status: 502 },
    );
  }

  const out = new Headers();
  for (const key of ["content-type", "cache-control", "content-length"]) {
    const value = upstream.headers.get(key);
    if (value) out.set(key, value);
  }
  // Proxies buffer text/event-stream by default, which would stall the thread.
  out.set("x-accel-buffering", "no");

  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
