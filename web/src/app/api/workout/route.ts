// Server-side proxy to workout-svc. The training log used to be a JSON
// document this route read and wrote itself; it now lives in Postgres behind
// its own service, and the wire shape is unchanged so the browser cannot tell.
//
// The bearer token stays here, and this route itself sits behind the console
// login (see src/middleware.ts).
//
// On Cloudflare there is no compose network to reach, so WORKOUT_URL has to
// point at whatever is published in front of the service - the same problem
// the agent proxy has, with the same answer (a tunnel).
const WORKOUT_URL = process.env.WORKOUT_URL ?? "http://localhost:8091";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function headersFor(request: Request, withBody: boolean): Headers {
  const headers = new Headers();
  const accept = request.headers.get("accept");
  if (accept) headers.set("accept", accept);
  if (withBody) headers.set("content-type", "application/json");
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  return headers;
}

async function forward(
  request: Request,
  method: "GET" | "PUT",
  body?: BodyInit,
): Promise<Response> {
  let upstream: Response;
  try {
    upstream = await fetch(`${WORKOUT_URL.replace(/\/$/, "")}/workout`, {
      method,
      headers: headersFor(request, method === "PUT"),
      body,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json(
      { error: "운동 기록 서비스에 연결하지 못했습니다." },
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
  return forward(request, "GET");
}

export async function PUT(request: Request) {
  return forward(request, "PUT", await request.text());
}

/**
 * The tab-close flush.
 *
 * `navigator.sendBeacon` can only issue POST, so a log flushed by closing the
 * tab arrived here as a POST and got a 405 from a route that only exported GET
 * and PUT - the last set of a session was being dropped, silently, exactly in
 * the case the flush existed to cover. Accepting POST as the same replace is
 * what makes that path work.
 */
export async function POST(request: Request) {
  return forward(request, "PUT", await request.text());
}
