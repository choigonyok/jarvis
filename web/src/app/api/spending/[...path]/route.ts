import { pathProxy } from "@/lib/proxy";

// spending-svc. Its routes are the book's own - nothing here reaches the
// KakaoTalk collector behind it.
const SPENDING_URL = process.env.SPENDING_URL ?? "http://localhost:8095";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(SPENDING_URL, { label: "가계부 서비스" });

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const PUT = proxy;
export const DELETE = proxy;
