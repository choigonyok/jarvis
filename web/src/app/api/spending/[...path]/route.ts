import { pathProxy } from "@/lib/proxy";

// The household book, which mind keeps. The gateway lets through only what the
// 가계부 screen does (spendingRoute).
const MIND_URL = process.env.MIND_URL ?? "http://localhost:8102";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(`${MIND_URL.replace(/\/$/, "")}/api/spending`, { label: "가계부" });

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const PUT = proxy;
export const DELETE = proxy;
