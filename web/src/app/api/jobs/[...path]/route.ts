import { pathProxy } from "@/lib/proxy";

// jobs-svc. It only records; the agent's scheduler is what runs a job.
const JOBS_URL = process.env.JOBS_URL ?? "http://localhost:8098";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(JOBS_URL, { label: "작업 서비스" });

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const PUT = proxy;
export const DELETE = proxy;
