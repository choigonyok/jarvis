// Server-side proxy for the principal ledger in assets-svc. Same reasoning as
// /api/portfolio: the browser never talks to that service directly.
const ASSETS_URL = process.env.ASSETS_URL ?? "http://localhost:8092";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function forward(method: string, path: string, body?: string): Promise<Response> {
  const headers = new Headers({ "content-type": "application/json" });
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);

  let upstream: Response;
  try {
    upstream = await fetch(`${ASSETS_URL.replace(/\/$/, "")}${path}`, {
      method,
      headers,
      body,
      cache: "no-store",
    });
  } catch {
    return Response.json({ error: "자산 서비스에 연결하지 못했습니다." }, { status: 502 });
  }
  return new Response(upstream.body, {
    status: upstream.status,
    headers: { "content-type": "application/json", "cache-control": "no-store" },
  });
}

export async function POST(request: Request) {
  return forward("POST", "/principal", await request.text());
}

/** DELETE /api/principal?id=12 */
export async function DELETE(request: Request) {
  const id = new URL(request.url).searchParams.get("id") ?? "";
  if (!/^\d+$/.test(id)) {
    return Response.json({ error: "id 가 필요합니다." }, { status: 400 });
  }
  return forward("DELETE", `/principal/${id}`);
}
