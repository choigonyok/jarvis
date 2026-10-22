/**
 * The backend gateway: <service>/<path> → that service on the Mac mini,
 * through a Workers VPC binding.
 *
 * It has two doors, and what decides which one a request came in by is
 * Cloudflare, not anything in the request:
 *
 * - Internal, the console Worker's service binding (web/wrangler.jsonc,
 *   entrypoint "Internal"). Every service, for a caller holding one of the
 *   console's tokens: the operator's (JARVIS_EDGE_TOKEN) opens everything,
 *   the guest's (JARVIS_EDGE_GUEST_TOKEN) only the guest's own agent, the
 *   workout log, the guest's notifications and the guest's own voice - so a
 *   hole in the console's proxy still cannot carry a guest to the operator's
 *   money, messages or voice.
 * - The public hostname, jarvis-be.choigonyok.com. Only what has to be
 *   reached from outside the console: the phone's doors - POST
 *   /context/owntracks and /context/signals (OwnTracks, iOS Shortcuts) -
 *   whose own credentials the service checks, and the 화면 tab's VNC socket, which
 *   carries a 60-second ticket the console Worker signs (a browser cannot put
 *   a header on a WebSocket; see ticketValid). A console token presented
 *   here opens nothing else: it would be one leaked secret away from every
 *   service, from anywhere on the internet.
 *
 * For the agent, only the routes the console uses. The agent also serves
 * /mcp (the tools Claude Code calls - unauthenticated, because they are meant
 * to be dialled over loopback) and /intercept (the payment guard's line).
 * Through here, either would be a way around the approval card.
 *
 * For mind, only what the 생활 screen reads and the operator's word on a
 * note (mindRoute). The rest of mind's /api replays the log, moves clocks,
 * approves intents and reads message text - the mind console's, over an ssh
 * tunnel, never this door's.
 */

import { WorkerEntrypoint } from "cloudflare:workers";

interface Fetcher {
  fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

interface Env {
  /** What the services behind check. Only this gateway holds it. */
  JARVIS_API_TOKEN: string;
  /** What the console Worker presents for the operator. */
  JARVIS_EDGE_TOKEN?: string;
  /** What it presents for the guest: opens GUEST_SERVICES only. */
  JARVIS_EDGE_GUEST_TOKEN?: string;
  /** What the guest's agent checks (and calls the services with), in place of JARVIS_API_TOKEN. */
  JARVIS_GUEST_API_TOKEN?: string;
  AGENT: Fetcher;
  WORKOUT: Fetcher;
  ASSETS: Fetcher;
  SPENDING: Fetcher;
  JOBS: Fetcher;
  KAKAOTALK: Fetcher;
  IMESSAGE: Fetcher;
  STATUS: Fetcher;
  BROWSER: Fetcher;
  AGENT_GUEST: Fetcher;
  IMMICH: Fetcher;
  NOTIFY: Fetcher;
  CONTEXT: Fetcher;
  TTS: Fetcher;
  MIND: Fetcher;
  /** Immich's API key for the gallery. Only this gateway holds it. */
  IMMICH_API_KEY?: string;
}

const SERVICES: Record<string, { binding: keyof Env; origin: string }> = {
  agent: { binding: "AGENT", origin: "http://agent:8080" },
  // The guest account's own agent (its own conversation, calendar only).
  "agent-guest": { binding: "AGENT_GUEST", origin: "http://agent-guest:8081" },
  workout: { binding: "WORKOUT", origin: "http://workout:8091" },
  assets: { binding: "ASSETS", origin: "http://assets:8092" },
  spending: { binding: "SPENDING", origin: "http://spending:8095" },
  jobs: { binding: "JOBS", origin: "http://jobs:8098" },
  kakaotalk: { binding: "KAKAOTALK", origin: "http://kakaotalk:8090" },
  imessage: { binding: "IMESSAGE", origin: "http://imessage:8099" },
  status: { binding: "STATUS", origin: "http://status:8096" },
  immich: { binding: "IMMICH", origin: "http://immich:2283" },
  // The inbox, its settings and this device's push subscription. Producers
  // reach notify-svc inside the compose network, not through here.
  notify: { binding: "NOTIFY", origin: "http://notify:8097" },
  context: { binding: "CONTEXT", origin: "http://context:8103" },
  // Each person's cloned voice, a process on the Mac mini itself (tts/server.py):
  // the tunnel reaches it at the Docker host's address. The token it is handed
  // decides whose recording a request touches (see below).
  tts: { binding: "TTS", origin: "http://host.docker.internal:8106" },
  vnc: { binding: "BROWSER", origin: "http://browser:6080" },
  // The notes and ledgers mind keeps about the operator (the 생활 screen).
  mind: { binding: "MIND", origin: "http://mind:8102" },
};

/** What the guest's token opens: their own agent, the workout log, their notifications, their own voice. */
const GUEST_SERVICES = new Set(["agent-guest", "workout", "notify", "tts"]);

/** Which agent must have answered, by route. */
const AGENT_ROLE: Record<string, string> = { agent: "owner", "agent-guest": "guest" };

/** The agent routes the console drives. Anything else on the agent is internal. */
const AGENT_ROUTES = new Set(["healthz", "thread", "messages", "events", "proposals", "calendar", "uploads", "usage", "vault"]);

/**
 * What the 생활 screen may do on mind: read the notes, the map, the things
 * owned and the meals, read a note's versions, and say a note is so, is not,
 * or reads otherwise.
 */
function mindRoute(method: string, path: string): boolean {
  if (method === "GET") {
    return /^api\/(notes|places|things|meals)$/.test(path) || /^api\/notes\/[A-Za-z0-9_-]+\/versions$/.test(path);
  }
  return method === "POST" && /^api\/notes\/[A-Za-z0-9_-]+$/.test(path);
}

function same(a: string, b: string): boolean {
  // Constant time: the comparison should not say how much of a guess was right.
  const x = new TextEncoder().encode(a);
  const y = new TextEncoder().encode(b);
  let diff = x.length ^ y.length;
  for (let i = 0; i < Math.max(x.length, y.length); i++) diff |= (x[i] ?? 0) ^ (y[i] ?? 0);
  return diff === 0;
}

const hex = (buf: ArrayBuffer) =>
  [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");

/**
 * "<expiry epoch seconds>.<hex HMAC-SHA256(token, 'vnc:' + expiry)>". The
 * console Worker mints it (web/src/app/api/screen/ticket); it only opens the
 * VNC socket, and only until it expires.
 */
async function ticketValid(ticket: string | null, token: string, now = Date.now()): Promise<boolean> {
  if (!ticket) return false;
  const [exp, sig] = ticket.split(".");
  if (!exp || !sig || !/^\d+$/.test(exp) || Number(exp) * 1000 < now) return false;
  const key = await crypto.subtle.importKey(
    "raw", new TextEncoder().encode(token), { name: "HMAC", hash: "SHA-256" }, false, ["sign"],
  );
  const want = hex(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`vnc:${exp}`)));
  return same(sig, want);
}

const json = (status: number, error: string) =>
  Response.json({ error }, { status, headers: { "cache-control": "no-store" } });

/** The public hostname: the phone's doors and the VNC socket, nothing else. */
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    return gateway(request, env, false);
  },
};

/** The console Worker's service binding: every service, by token. */
export class Internal extends WorkerEntrypoint<Env> {
  async fetch(request: Request): Promise<Response> {
    return gateway(request, this.env, true);
  }
}

async function gateway(request: Request, env: Env, internal: boolean): Promise<Response> {
  if (!env.JARVIS_API_TOKEN || !env.JARVIS_EDGE_TOKEN) return json(503, "게이트웨이에 토큰이 설정되지 않았습니다.");
  const url = new URL(request.url);
  const [, name = "", ...rest] = url.pathname.split("/");

  // Callers hold the console's tokens, not the services': a leaked console
  // secret opens this door, which can be rotated alone, and nothing behind.
  // Neither is taken on the public hostname.
  const auth = request.headers.get("authorization") ?? "";
  const owner = internal && same(auth, `Bearer ${env.JARVIS_EDGE_TOKEN}`);
  const guest =
    internal && !owner && !!env.JARVIS_EDGE_GUEST_TOKEN && same(auth, `Bearer ${env.JARVIS_EDGE_GUEST_TOKEN}`);
  const bearer = owner || guest;
  if (name === "vnc") {
    // The socket and nothing else: noVNC's own pages are not needed.
    if (guest || rest.join("/") !== "websockify") return json(404, "그런 경로가 없습니다.");
    if (!owner && !(await ticketValid(url.searchParams.get("ticket"), env.JARVIS_EDGE_TOKEN))) {
      return json(401, "입장권이 없거나 만료됐습니다.");
    }
    url.searchParams.delete("ticket");
    // Handed over whole, so the WebSocket upgrade goes through as one.
    return (env.BROWSER as Fetcher).fetch(new Request(`http://browser:6080/websockify${url.search}`, request));
  }
  // The phone's own doors: their credentials are checked by the service (context-svc).
  const phone =
    request.method === "POST" && rest.length === 1 && name === "context" && (rest[0] === "owntracks" || rest[0] === "signals");
  if (!internal && !phone) return json(404, "그런 경로가 없습니다.");
  if (!bearer && !phone) return json(401, "인증이 필요합니다.");

  const service = SERVICES[name];
  if (!service) return json(404, "그런 서비스가 없습니다.");
  if (guest && !GUEST_SERVICES.has(name)) return json(404, "그런 서비스가 없습니다.");
  if ((name === "agent" || name === "agent-guest") && !AGENT_ROUTES.has(rest[0] ?? "")) {
    return json(404, "그런 경로가 없습니다.");
  }
  if (name === "mind" && !mindRoute(request.method, rest.join("/"))) {
    return json(404, "그런 경로가 없습니다.");
  }

  // The gallery reads Immich and searches it, nothing else: no upload,
  // no delete, no settings - the phone app does those, through Access.
  if (name === "immich") {
    const path = rest.join("/");
    const search = request.method === "POST" && /^api\/search\/(smart|metadata)$/.test(path);
    if (!(request.method === "GET" || request.method === "HEAD" || search) || !path.startsWith("api/")) {
      return json(405, "갤러리는 읽기만 합니다.");
    }
    if (!env.IMMICH_API_KEY) return json(503, "IMMICH_API_KEY 가 설정되지 않았습니다.");
  }

  const target = `${service.origin}/${rest.join("/")}${url.search}`;
  const headers = new Headers(request.headers);
  headers.delete("host");
  if (guest) {
    // Nothing a guest's request says about who is asking is taken at its
    // word: the notifications it touches are the guest's.
    for (const key of [...headers.keys()]) if (key.startsWith("x-jarvis-")) headers.delete(key);
    if (name === "notify") headers.set("x-jarvis-audience", "guest");
  }
  // The services want their own token; the caller's never goes past here.
  // The guest's agent has a token of its own, which opens only the guest's
  // side of the services it calls. The voice service keeps one recording per
  // token, so the guest's requests go on with the guest's token and nothing
  // else: without it there is no guest voice, never the operator's.
  if (bearer) {
    if (guest && name === "tts" && !env.JARVIS_GUEST_API_TOKEN) return json(503, "손님 목소리가 설정되지 않았습니다.");
    const own = (name === "agent-guest" || (guest && name === "tts")) && env.JARVIS_GUEST_API_TOKEN;
    const token = own ? env.JARVIS_GUEST_API_TOKEN! : env.JARVIS_API_TOKEN;
    headers.set("authorization", `Bearer ${token}`);
  }
  if (name === "immich") {
    // Immich would read our bearer as one of its own sessions and refuse it.
    headers.delete("authorization");
    headers.set("x-api-key", env.IMMICH_API_KEY!);
  }
  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  // Both agents ride one tunnel, and a pooled connection has been seen to
  // land on the other one. Each agent names itself on every response; one
  // from the wrong agent is dropped here, never passed on - the guest must
  // not be handed the operator's conversation, nor the other way round.
  // (The wrong agent also refuses the other's token, so nothing was done.)
  // A read is asked again: a 502 handed to the console's event stream closed
  // it for good, and the thread then showed nothing sent until the app was
  // restarted (2026-10-22). A request with a body cannot be sent twice.
  const expect = AGENT_ROLE[name];
  const tries = expect && !hasBody ? 3 : 1;
  try {
    for (let attempt = 1; ; attempt++) {
      // The response is handed back as it arrives - /agent/events is a stream.
      const res = await (env[service.binding] as Fetcher).fetch(target, {
        method: request.method,
        headers,
        body: hasBody ? request.body : undefined,
        redirect: "manual",
      });
      if (!expect || res.headers.get("x-jarvis-agent") === expect) return res;
      await res.body?.cancel();
      console.warn("다른 jarvis 가 응답했습니다", { service: name, path: rest.join("/"), attempt, of: tries });
      if (attempt >= tries) return json(502, "다른 jarvis 가 응답했습니다. 잠시 뒤 다시 시도하세요.");
    }
  } catch {
    return json(502, `${name} 서비스에 닿지 못했습니다. 맥미니와 터널을 확인하세요.`);
  }
}
