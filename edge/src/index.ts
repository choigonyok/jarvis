/**
 * The backend gateway: jarvis-be.choigonyok.com/<service>/<path> → that
 * service on the Mac mini, through a Workers VPC binding.
 *
 * Two checks happen here before anything leaves Cloudflare:
 *
 * - The bearer token. The console Worker holds it; nobody else does. A
 *   request without it never reaches the tunnel - the services check it too,
 *   but they should not be the first thing a stranger can knock on.
 * - /vnc/websockify (the 화면 tab) may instead carry a ticket in its query:
 *   a browser cannot put a header on a WebSocket. The console Worker signs
 *   the ticket with the same token, for 60 seconds - see ticketValid.
 * - For the agent, only the routes the console uses. The agent also serves
 *   /mcp (the tools Claude Code calls - unauthenticated, because they are
 *   meant to be dialled over loopback) and /intercept (the payment guard's
 *   line). Through here, either would be a way around the approval card.
 */

interface Fetcher {
  fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

interface Env {
  JARVIS_API_TOKEN: string;
  AGENT: Fetcher;
  WORKOUT: Fetcher;
  ASSETS: Fetcher;
  SPENDING: Fetcher;
  MARKET: Fetcher;
  KAKAOTALK: Fetcher;
  STATUS: Fetcher;
  BROWSER: Fetcher;
  AGENT_GUEST: Fetcher;
}

const SERVICES: Record<string, { binding: keyof Env; origin: string }> = {
  agent: { binding: "AGENT", origin: "http://agent:8080" },
  // The guest account's own agent (its own conversation, calendar only).
  "agent-guest": { binding: "AGENT_GUEST", origin: "http://agent-guest:8081" },
  workout: { binding: "WORKOUT", origin: "http://workout:8091" },
  assets: { binding: "ASSETS", origin: "http://assets:8092" },
  spending: { binding: "SPENDING", origin: "http://spending:8095" },
  market: { binding: "MARKET", origin: "http://market:8097" },
  kakaotalk: { binding: "KAKAOTALK", origin: "http://kakaotalk:8090" },
  status: { binding: "STATUS", origin: "http://status:8096" },
  vnc: { binding: "BROWSER", origin: "http://browser:6080" },
};

/** Which agent must have answered, by route. */
const AGENT_ROLE: Record<string, string> = { agent: "owner", "agent-guest": "guest" };

/** The agent routes the console drives. Anything else on the agent is internal. */
const AGENT_ROUTES = new Set(["healthz", "thread", "messages", "events", "proposals", "calendar", "uploads"]);

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

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (!env.JARVIS_API_TOKEN) return json(503, "게이트웨이에 토큰이 설정되지 않았습니다.");
    const url = new URL(request.url);
    const [, name = "", ...rest] = url.pathname.split("/");

    const auth = request.headers.get("authorization") ?? "";
    const bearer = same(auth, `Bearer ${env.JARVIS_API_TOKEN}`);
    if (name === "vnc") {
      // The socket and nothing else: noVNC's own pages are not needed.
      if (rest.join("/") !== "websockify") return json(404, "그런 경로가 없습니다.");
      if (!bearer && !(await ticketValid(url.searchParams.get("ticket"), env.JARVIS_API_TOKEN))) {
        return json(401, "입장권이 없거나 만료됐습니다.");
      }
      url.searchParams.delete("ticket");
      // Handed over whole, so the WebSocket upgrade goes through as one.
      return (env.BROWSER as Fetcher).fetch(new Request(`http://browser:6080/websockify${url.search}`, request));
    }
    if (!bearer) return json(401, "인증이 필요합니다.");

    const service = SERVICES[name];
    if (!service) return json(404, "그런 서비스가 없습니다.");
    if ((name === "agent" || name === "agent-guest") && !AGENT_ROUTES.has(rest[0] ?? "")) {
      return json(404, "그런 경로가 없습니다.");
    }

    const target = `${service.origin}/${rest.join("/")}${url.search}`;
    const headers = new Headers(request.headers);
    headers.delete("host");
    const hasBody = request.method !== "GET" && request.method !== "HEAD";
    try {
      // The response is handed back as it arrives - /agent/events is a stream.
      const res = await (env[service.binding] as Fetcher).fetch(target, {
        method: request.method,
        headers,
        body: hasBody ? request.body : undefined,
        redirect: "manual",
      });
      // Both agents ride one tunnel, and a pooled connection has been seen to
      // land on the other one. Each agent names itself on every response; one
      // from the wrong agent is dropped here, never passed on - the guest must
      // not be handed the operator's conversation, nor the other way round.
      const expect = AGENT_ROLE[name];
      if (expect && res.headers.get("x-jarvis-agent") !== expect) {
        await res.body?.cancel();
        return json(502, "다른 jarvis 가 응답했습니다. 잠시 뒤 다시 시도하세요.");
      }
      return res;
    } catch {
      return json(502, `${name} 서비스에 닿지 못했습니다. 맥미니와 터널을 확인하세요.`);
    }
  },
};
