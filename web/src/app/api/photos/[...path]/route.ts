// Server-side proxy to Immich, through the gateway (which holds Immich's API
// key and only lets reads and searches through). /api/photos/<path> →
// <PHOTOS_URL>/<path>. Thumbnails, originals and video stream straight
// through, Range requests included, so a video can be scrubbed.
const PHOTOS_URL = process.env.PHOTOS_URL ?? "http://localhost:2283";
const API_TOKEN = process.env.JARVIS_API_TOKEN ?? "";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(request: Request, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  const target = `${PHOTOS_URL.replace(/\/$/, "")}/${path.map(encodeURIComponent).join("/")}${new URL(request.url).search}`;

  const headers = new Headers();
  if (API_TOKEN) headers.set("authorization", `Bearer ${API_TOKEN}`);
  for (const key of ["range", "accept", "content-type", "if-none-match"]) {
    const value = request.headers.get(key);
    if (value) headers.set(key, value);
  }

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      body: request.method === "POST" ? await request.text() : undefined,
      cache: "no-store",
      signal: request.signal,
    });
  } catch {
    return Response.json({ error: "사진 서버에 연결하지 못했습니다." }, { status: 502 });
  }

  const out = new Headers();
  for (const key of ["content-type", "content-length", "content-range", "accept-ranges", "etag", "cache-control", "content-disposition"]) {
    const value = upstream.headers.get(key);
    if (value) out.set(key, value);
  }
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
