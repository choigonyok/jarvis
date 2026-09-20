// The browser never talks to the agent directly: the API key and the agent's
// surface stay on the server side of this proxy.
const AGENT_URL = process.env.AGENT_URL ?? "http://localhost:8080";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  request: Request,
  context: { params: Promise<{ path: string[] }> },
) {
  const { path } = await context.params;
  const target = new URL(
    `${AGENT_URL.replace(/\/$/, "")}/${path.join("/")}`,
  );
  target.search = new URL(request.url).search;

  const headers = new Headers();
  const contentType = request.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);
  const accept = request.headers.get("accept");
  if (accept) headers.set("accept", accept);

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      body: request.method === "GET" ? undefined : await request.text(),
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
  for (const key of ["content-type", "cache-control"]) {
    const value = upstream.headers.get(key);
    if (value) out.set(key, value);
  }
  // Proxies buffer text/event-stream by default, which would stall the thread.
  out.set("x-accel-buffering", "no");

  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
