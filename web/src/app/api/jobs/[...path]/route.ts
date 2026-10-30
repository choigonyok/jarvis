import { MIND_URL, MIND_WRITES, pathProxy } from "@/lib/proxy";

// The jobs, which mind keeps: read there, changed at the way in. The agent's
// scheduler is what runs one. The gateway lets through only what the 작업 tab
// does (jobsRoute).
export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const GET = pathProxy(`${MIND_URL}/api/jobs`, { label: "작업" });
export const POST = pathProxy(`${MIND_WRITES}/api/jobs`, { label: "작업" });
