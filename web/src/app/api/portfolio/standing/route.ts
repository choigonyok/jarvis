import { forward } from "@/lib/proxy";

// Standing orders through assets-svc: GET the list, POST a new one. The
// operator's only - the middleware keeps a guest off /api/portfolio.
const ASSETS_URL = (process.env.ASSETS_URL ?? "http://localhost:8092").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  return forward(request, `${ASSETS_URL}/standing`, { label: "자산 서비스", requestHeaders: [] }, { method: "GET" });
}

export async function POST(request: Request) {
  return forward(request, `${ASSETS_URL}/standing`, { label: "자산 서비스" });
}
