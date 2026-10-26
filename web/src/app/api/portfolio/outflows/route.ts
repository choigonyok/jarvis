import { forward } from "@/lib/proxy";

// What was said about where a withdrawal went, through assets-svc: DELETE
// forgets it (?key=), so the agent's card asks again. The answer itself is
// given on that card. The operator's only - the middleware keeps a guest off
// /api/portfolio.
const ASSETS_URL = (process.env.ASSETS_URL ?? "http://localhost:8092").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function DELETE(request: Request) {
  const key = new URL(request.url).searchParams.get("key") ?? "";
  if (!key) return Response.json({ error: "어느 출금인지(key) 알려 주세요." }, { status: 400 });
  return forward(
    request,
    `${ASSETS_URL}/outflows/answer?key=${encodeURIComponent(key)}`,
    { label: "자산 서비스", requestHeaders: [] },
    { method: "DELETE", body: null },
  );
}
