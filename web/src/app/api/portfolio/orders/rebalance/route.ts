import { forward } from "@/lib/proxy";

// The current rebalance plan through assets-svc, checked or placed.
const ASSETS_URL = (process.env.ASSETS_URL ?? "http://localhost:8092").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  return forward(request, `${ASSETS_URL}/orders/rebalance`, { label: "자산 서비스" });
}
