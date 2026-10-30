// The one server-side proxy every /api/<service> route goes through.
//
// The browser never talks to a service directly: the bearer token stays on
// the server side of this, and every route using it sits behind the console
// login (see src/middleware.ts). Responses are never cached.
import { getCloudflareContext } from "@opennextjs/cloudflare";
import { ROLE_HEADER } from "@/lib/role";

const GATEWAY_TOKEN = process.env.JARVIS_EDGE_TOKEN ?? "";
// The guest's own: the gateway opens only the guest's services for it, so a
// request this proxy mis-routes still cannot carry a guest anywhere else.
const GATEWAY_GUEST_TOKEN = process.env.JARVIS_EDGE_GUEST_TOKEN ?? "";
const GATEWAY_ORIGIN = "https://jarvis-be.choigonyok.com";

/** Where mind is read. */
export const MIND_URL = (process.env.MIND_URL ?? "http://localhost:8102").replace(/\/$/, "");
/** Where what a screen changes in mind goes in: as a record, at the way in. */
export const MIND_WRITES = `${(process.env.INPUT_URL ?? "http://localhost:8101").replace(/\/$/, "")}/mind`;

/**
 * fetch, but a request for the gateway goes over the GATEWAY service binding
 * when there is one (on Cloudflare): the gateway's services are reached only
 * that way - its public hostname answers just the phone's doors. Anywhere
 * else - next dev on a laptop - it is a plain fetch.
 */
function gatewayFetch(target: string, init: RequestInit): Promise<Response> {
  if (target.startsWith(GATEWAY_ORIGIN)) {
    try {
      const binding = (getCloudflareContext().env as { GATEWAY?: { fetch: typeof fetch } }).GATEWAY;
      if (binding) return binding.fetch(target, init);
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
  const guest = request.headers.get(ROLE_HEADER) === "guest";
  if (guest && !GATEWAY_GUEST_TOKEN) {
    return Response.json({ error: "손님용 연결이 설정되지 않았습니다." }, { status: 503 });
  }
  const token = guest ? GATEWAY_GUEST_TOKEN : GATEWAY_TOKEN;
  if (token) headers.set("authorization", `Bearer ${token}`);

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
 * A segment that could step out of the service it is meant for once joined:
 * Next decodes each catch-all segment, so "..%2Fstatus" arrives as
 * "../status" - one segment holding a separator and a parent step.
 */
const unsafeSegment = (s: string) => s === "" || s === "." || s === ".." || /[/\\]/.test(s);

/**
 * A catch-all route handler: /api/<x>/<path> → <base>/<path>?<search>.
 * base may depend on the request (the guest's own agent); returning a
 * Response from it answers instead. allow, when given, limits the paths.
 * Every segment is encoded again, and one that is ".", ".." or holds a
 * slash is refused rather than sent.
 */
export function pathProxy(
  base: string | ((request: Request) => string | Response),
  opts: ProxyOptions & { allow?: (path: string) => boolean },
) {
  return async (request: Request, context: Ctx): Promise<Response> => {
    const { path } = await context.params;
    if (path.some(unsafeSegment)) {
      return Response.json({ error: "없는 경로입니다." }, { status: 404 });
    }
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
