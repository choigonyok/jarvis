import { MIND_URL, MIND_WRITES, pathProxy } from "@/lib/proxy";

// The household book, which mind keeps: read there, changed at the way in. The
// gateway lets through only what the 가계부 screen does (spendingRoute).
export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const read = pathProxy(`${MIND_URL}/api/spending`, { label: "가계부" });
const write = pathProxy(`${MIND_WRITES}/api/spending`, { label: "가계부" });

export const GET = read;
export const POST = write;
export const PATCH = write;
export const PUT = write;
export const DELETE = write;
