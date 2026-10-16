import { pathProxy } from "@/lib/proxy";

// status-svc. Read-only apart from /refresh, which only makes it look again.
const STATUS_URL = process.env.STATUS_URL ?? "http://localhost:8096";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(STATUS_URL, { label: "상태 서비스" });

export const GET = proxy;
export const POST = proxy;
