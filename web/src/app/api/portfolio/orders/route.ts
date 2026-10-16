import { forward } from "@/lib/proxy";

// Orders through assets-svc: GET the record, POST orders a person confirmed
// on the 자산 screen (or a dry run of them). The operator's only - the
// middleware keeps a guest off /api/portfolio.
const ASSETS_URL = (process.env.ASSETS_URL ?? "http://localhost:8092").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const limit = Number(new URL(request.url).searchParams.get("limit") ?? 20) || 20;
  return forward(request, `${ASSETS_URL}/orders?limit=${Math.min(100, Math.max(1, limit))}`, { label: "자산 서비스", requestHeaders: [] }, { method: "GET" });
}

export async function POST(request: Request) {
  return forward(request, `${ASSETS_URL}/orders`, { label: "자산 서비스" });
}
