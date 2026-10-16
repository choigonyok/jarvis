import { pathProxy } from "@/lib/proxy";

// The iMessage collector. Read-only surface: GET only.
const IMESSAGE_URL = process.env.IMESSAGE_URL ?? "http://localhost:8099";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(IMESSAGE_URL, { label: "문자 수집기" });

export const GET = proxy;
