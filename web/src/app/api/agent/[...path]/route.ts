import { ROLE_HEADER } from "@/lib/role";
import { pathProxy } from "@/lib/proxy";

// The browser never talks to the agent directly: the API key and the agent's
// surface stay on the server side of this proxy. The guest account talks to
// its own agent (its own conversation, calendar only); the middleware decides
// who is a guest and stamps the request.
const AGENT_URL = process.env.AGENT_URL ?? "http://localhost:8080";
const AGENT_GUEST_URL = process.env.AGENT_GUEST_URL ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(
  (request) => {
    if (request.headers.get(ROLE_HEADER) !== "guest") return AGENT_URL;
    return AGENT_GUEST_URL || Response.json({ error: "손님용 jarvis 가 설정되지 않았습니다." }, { status: 503 });
  },
  { label: "에이전트", responseHeaders: ["content-length"] },
);

export const GET = proxy;
export const POST = proxy;
// Forgetting a saved login (/vault/{domain}) and removing a calendar entry.
export const DELETE = proxy;
