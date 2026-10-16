// The one server-side proxy every /api/<service> route goes through.
//
// The browser never talks to a service directly: the bearer token stays on
// the server side of this, and every route using it sits behind the console
// login (see src/middleware.ts). Responses are never cached.
import { getCloudflareContext } from "@opennextjs/cloudflare";

const GATEWAY_TOKEN = process.env.JARVIS_EDGE_TOKEN ?? "";
const GATEWAY_ORIGIN = "https://jarvis-be.choigonyok.com";

/**
 * fetch, but a request for the gateway goes over the GATEWAY service binding
 * when there is one (on Cloudflare): no public hop, no DNS, no TLS. Anywhere
 * else - next dev on a laptop - it is a plain fetch.
 */
function gatewayFetch(target: string, init: RequestInit): Promise<Response> {
  if (target.startsWith(GATEWAY_ORIGIN)) {
    try {
      const binding = (getCloudflareContext().env as { GATEWAY?: { fetch: typeof fetch } }).GATEWAY;
      // A binding that fails outright (not deployed, renamed) falls back to the
      // public hostname rather than taking the console down with it. The body
      // is bytes, so it can be sent twice.
      if (binding) return binding.fetch(target, init).catch(() => fetch(target, init));
    } catch {
      // not running on Cloudflare
    }
  }
  return fetch(target, init);
}

export type ProxyOptions = {
  /** Shown when the service cannot be reached: "<label>에 연결하지 못했습니다." */
  label: string;
  /** Request headers passed on, besides the bearer token. */
  requestHeaders?: string[];
  /** Response headers passed back. content-type always is. */
  responseHeaders?: string[];
  /** Extra headers for the upstream request. */
  extra?: (request: Request) => HeadersInit | undefined;
};

/** Forward request to target (absolute URL) with the bearer token. */
export async function forward(
  request: Request,
  target: string,
  opts: ProxyOptions,
  init?: { method?: string; body?: BodyInit | null },
): Promise<Response> {
  const headers = new Headers(opts.extra?.(request));
  for (const key of opts.requestHeaders ?? ["content-type", "accept"]) {
    const value = request.headers.get(key);
    if (value) headers.set(key, value);
  }
  if (GATEWAY_TOKEN) headers.set("authorization", `Bearer ${GATEWAY_TOKEN}`);

  const method = init?.method ?? request.method;
  let body = init?.body;
  if (body === undefined && method !== "GET" && method !== "HEAD") {
    // Bytes, not text: a photo upload passes through here too.
    body = await request.arrayBuffer();
  }

  let upstream: Response;
  try {
    upstream = await gatewayFetch(target, {
      method,
      headers,
      body,
      cache: "no-store",
      // An event stream stays open while the page does; closing it closes this.
      signal: request.signal,
    });
  } catch {
    return Response.json({ error: `${opts.label}에 연결하지 못했습니다.` }, { status: 502 });
  }

  const out = new Headers({ "cache-control": "no-store" });
  for (const key of ["content-type", ...(opts.responseHeaders ?? [])]) {
    const value = upstream.headers.get(key);
    if (value) out.set(key, value);
  }
  // Proxies buffer text/event-stream by default, which would stall a stream.
  if (out.get("content-type")?.startsWith("text/event-stream")) out.set("x-accel-buffering", "no");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

type Ctx = { params: Promise<{ path: string[] }> };

/**
 * A catch-all route handler: /api/<x>/<path> → <base>/<path>?<search>.
 * base may depend on the request (the guest's own agent); returning a
 * Response from it answers instead. allow, when given, limits the paths.
 */
export function pathProxy(
  base: string | ((request: Request) => string | Response),
  opts: ProxyOptions & { allow?: (path: string) => boolean },
) {
  return async (request: Request, context: Ctx): Promise<Response> => {
    const { path } = await context.params;
    const joined = path.map(encodeURIComponent).join("/");
    if (opts.allow && !opts.allow(path.join("/"))) {
      return Response.json({ error: "없는 경로입니다." }, { status: 404 });
    }
    const root = typeof base === "string" ? base : base(request);
    if (root instanceof Response) return root;
    const target = `${root.replace(/\/$/, "")}/${joined}${new URL(request.url).search}`;
    return forward(request, target, opts);
  };
}
