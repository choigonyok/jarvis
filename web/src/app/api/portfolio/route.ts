// Server-side proxy to assets-svc.
//
// This route used to call the brokerages itself, which meant the Upbit and KIS
// keys lived in this process. They are in assets-svc now: the keys can place
// orders, and the surface that serves HTML to a browser is the wrong place for
// them to sit. Nothing about the response shape changed.
//
// On Cloudflare there is no compose network, so ASSETS_URL has to point at
// whatever is published in front of the service.
const ASSETS_URL = process.env.ASSETS_URL ?? "http://localhost:8092";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function forward(request: Request, path: string): Promise<Response> {
  const headers = new Headers();
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);

  let upstream: Response;
  try {
    upstream = await fetch(`${ASSETS_URL.replace(/\/$/, "")}${path}`, {
      method: "GET",
      headers,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json(
      { error: "자산 서비스에 연결하지 못했습니다." },
      { status: 502 },
    );
  }

  const out = new Headers();
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  out.set("cache-control", "no-store");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export async function GET(request: Request) {
  // ?history=90 으로 기록된 일별 총액을 요청할 수 있다. 지금 화면은 쓰지
  // 않는다 - 곡선은 과거 종가로 현재 바스켓을 다시 매겨서 그리고, 그쪽이
  // 입출금을 수익률로 착각하지 않는다. 스냅샷이 답하는 것은 "그때 실제로
  // 무엇을 들고 있었나"라는 다른 질문이다.
  const params = new URL(request.url).searchParams;
  const days = params.get("history");
  return forward(
    request,
    days
      ? `/history?days=${encodeURIComponent(days)}`
      : params.get("fresh") === "1"
        ? "/portfolio?fresh=1"
        : "/portfolio",
  );
}
