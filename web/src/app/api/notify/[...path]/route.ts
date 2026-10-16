import { ROLE_HEADER } from "@/lib/role";
import { pathProxy } from "@/lib/proxy";

// notify-svc, through the gateway. /stream stays open while the page does.
//
// Each person reads their own: the inbox, devices and settings a request
// touches are the signed-in role's (x-jarvis-audience), which the middleware
// stamped - a browser's own header of that name is never passed on.
const NOTIFY_URL = process.env.NOTIFY_URL ?? "http://localhost:8097";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(NOTIFY_URL, {
  label: "알림 서비스",
  extra: (request) => {
    const headers: Record<string, string> = {
      "x-jarvis-audience": request.headers.get(ROLE_HEADER) === "guest" ? "guest" : "owner",
    };
    const ua = request.headers.get("x-client-user-agent") ?? request.headers.get("user-agent");
    if (ua) headers["x-client-user-agent"] = ua;
    return headers;
  },
});

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
