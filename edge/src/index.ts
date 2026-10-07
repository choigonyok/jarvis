/**
 * The backend gateway: jarvis-be.choigonyok.com/<service>/<path> → that
 * service on the Mac mini, through a Workers VPC binding.
 *
 * Two checks happen here before anything leaves Cloudflare:
 *
 * - The bearer token. The console Worker holds it; nobody else does. A
 *   request without it never reaches the tunnel - the services check it too,
 *   but they should not be the first thing a stranger can knock on.
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
  KAKAOTALK: Fetcher;
  STATUS: Fetcher;
}

const SERVICES: Record<string, { binding: keyof Env; origin: string }> = {
  agent: { binding: "AGENT", origin: "http://agent:8080" },
  workout: { binding: "WORKOUT", origin: "http://workout:8091" },
  assets: { binding: "ASSETS", origin: "http://assets:8092" },
  spending: { binding: "SPENDING", origin: "http://spending:8095" },
  kakaotalk: { binding: "KAKAOTALK", origin: "http://kakaotalk:8090" },
  status: { binding: "STATUS", origin: "http://status:8096" },
};

/** The agent routes the console drives. Anything else on the agent is internal. */
const AGENT_ROUTES = new Set(["healthz", "thread", "messages", "events", "proposals", "calendar"]);

function same(a: string, b: string): boolean {
  // Constant time: the comparison should not say how much of a guess was right.
  const x = new TextEncoder().encode(a);
  const y = new TextEncoder().encode(b);
  let diff = x.length ^ y.length;
  for (let i = 0; i < Math.max(x.length, y.length); i++) diff |= (x[i] ?? 0) ^ (y[i] ?? 0);
  return diff === 0;
}

const json = (status: number, error: string) =>
  Response.json({ error }, { status, headers: { "cache-control": "no-store" } });

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (!env.JARVIS_API_TOKEN) return json(503, "게이트웨이에 토큰이 설정되지 않았습니다.");
    const auth = request.headers.get("authorization") ?? "";
    if (!same(auth, `Bearer ${env.JARVIS_API_TOKEN}`)) return json(401, "인증이 필요합니다.");

    const url = new URL(request.url);
    const [, name = "", ...rest] = url.pathname.split("/");
    const service = SERVICES[name];
    if (!service) return json(404, "그런 서비스가 없습니다.");
    if (name === "agent" && !AGENT_ROUTES.has(rest[0] ?? "")) return json(404, "그런 경로가 없습니다.");

    const target = `${service.origin}/${rest.join("/")}${url.search}`;
    const headers = new Headers(request.headers);
    headers.delete("host");
    const hasBody = request.method !== "GET" && request.method !== "HEAD";
    try {
      // The response is handed back as it arrives - /agent/events is a stream.
      return await (env[service.binding] as Fetcher).fetch(target, {
        method: request.method,
        headers,
        body: hasBody ? request.body : undefined,
        redirect: "manual",
      });
    } catch {
      return json(502, `${name} 서비스에 닿지 못했습니다. 맥미니와 터널을 확인하세요.`);
    }
  },
};
