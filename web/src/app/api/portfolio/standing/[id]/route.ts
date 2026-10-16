import { forward } from "@/lib/proxy";

// Cancel a standing order (and today's order at the broker, if it is out).
const ASSETS_URL = (process.env.ASSETS_URL ?? "http://localhost:8092").replace(/\/$/, "");

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function DELETE(request: Request, ctx: { params: Promise<{ id: string }> }) {
  const { id } = await ctx.params;
  if (!/^\d+$/.test(id)) return Response.json({ error: "잘못된 주문 번호입니다." }, { status: 400 });
  return forward(request, `${ASSETS_URL}/standing/${id}`, { label: "자산 서비스", requestHeaders: [] }, { method: "DELETE", body: null });
}
