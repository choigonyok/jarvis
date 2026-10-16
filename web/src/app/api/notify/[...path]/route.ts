import { pathProxy } from "@/lib/proxy";

// notify-svc, through the gateway. The owner's only (lib/role.ts keeps a guest
// out). /stream stays open while the page does.
const NOTIFY_URL = process.env.NOTIFY_URL ?? "http://localhost:8097";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(NOTIFY_URL, {
  label: "알림 서비스",
  extra: (request) => {
    const ua = request.headers.get("x-client-user-agent") ?? request.headers.get("user-agent");
    return ua ? { "x-client-user-agent": ua } : undefined;
  },
});

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
