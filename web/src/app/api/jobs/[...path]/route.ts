import { pathProxy } from "@/lib/proxy";

// The jobs, which mind keeps; the agent's scheduler is what runs one. The
// gateway lets through only what the 작업 tab does (jobsRoute).
const MIND_URL = process.env.MIND_URL ?? "http://localhost:8102";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(`${MIND_URL.replace(/\/$/, "")}/api/jobs`, { label: "작업" });

export const GET = proxy;
export const POST = proxy;
