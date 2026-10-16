// Server-only helper shared by the /api/workout routes.
const WORKOUT_URL = process.env.WORKOUT_URL ?? "http://localhost:8091";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

function headersFor(request: Request, withBody: boolean): Headers {
  const headers = new Headers();
  const accept = request.headers.get("accept");
  if (accept) headers.set("accept", accept);
  if (withBody) headers.set("content-type", "application/json");
  const ifMatch = request.headers.get("if-match");
  if (ifMatch) headers.set("if-match", ifMatch);
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  return headers;
}

export async function forwardWorkout(
  request: Request,
  method: "GET" | "PUT" | "POST",
  body?: BodyInit,
  path = "/workout",
): Promise<Response> {
  let upstream: Response;
  try {
    upstream = await fetch(`${WORKOUT_URL.replace(/\/$/, "")}${path}`, {
      method,
      headers: headersFor(request, method !== "GET"),
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
  const etag = upstream.headers.get("etag");
  if (etag) out.set("etag", etag);
  out.set("cache-control", "no-store");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}
