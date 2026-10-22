// The 위치 screen's base map: OpenStreetMap as vector tiles from OpenFreeMap
// (no key, no account). The browser asks here, not the tile server: what is
// looked at is the operator's own day, and the tile server then sees this
// console's server rather than which streets someone looked at, from where.
// Cloudflare keeps each tile, and the browser keeps it too - a tileset's
// build is in its path, so a tile at a path never changes.
//
//   /api/map/planet                               the tileset (TileJSON), its tiles pointed back here
//   /api/map/tiles/<build>/<z>/<x>/<y>.pbf         one tile
const ORIGIN = "https://tiles.openfreemap.org";
const BUILD = /^\d{8}_\d{6}_pt$/;

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

type Ctx = { params: Promise<{ path: string[] }> };

/** On Cloudflare, how long its cache keeps what was fetched. Ignored anywhere else. */
const kept = (seconds: number) => ({ cf: { cacheEverything: true, cacheTtl: seconds } }) as RequestInit;

const missing = () => Response.json({ error: "없는 경로입니다." }, { status: 404 });

export async function GET(request: Request, context: Ctx): Promise<Response> {
  const { path } = await context.params;
  if (path.length === 1 && path[0] === "planet") return tileset(new URL(request.url).origin);
  if (path.length !== 5 || path[0] !== "tiles") return missing();
  const [, build, z, x, file] = path;
  const y = file.match(/^(\d{1,6})\.pbf$/)?.[1];
  if (!BUILD.test(build) || !/^\d{1,2}$/.test(z) || Number(z) > 14 || !/^\d{1,6}$/.test(x) || !y) return missing();

  let upstream: Response;
  try {
    upstream = await fetch(`${ORIGIN}/planet/${build}/${z}/${x}/${y}.pbf`, kept(30 * 86_400));
  } catch {
    return Response.json({ error: "지도 타일에 연결하지 못했습니다." }, { status: 502 });
  }
  if (!upstream.ok) return new Response(null, { status: upstream.status === 404 ? 404 : 502 });
  // The body comes decompressed; it goes on without a content-encoding, and
  // Cloudflare compresses protobuf on the way out.
  return new Response(upstream.body, {
    headers: {
      "content-type": "application/x-protobuf",
      "cache-control": "private, max-age=2592000, immutable",
    },
  });
}

/** The tileset as OpenFreeMap names it today, with its tiles fetched through here. */
async function tileset(origin: string): Promise<Response> {
  let body: { tiles?: string[] } & Record<string, unknown>;
  try {
    const res = await fetch(`${ORIGIN}/planet`, kept(3600));
    if (!res.ok) throw new Error(String(res.status));
    body = await res.json();
  } catch {
    return Response.json({ error: "지도 타일 정보를 가져오지 못했습니다." }, { status: 502 });
  }
  const tiles = (body.tiles ?? []).flatMap((url) => {
    const build = url.match(/\/planet\/([^/]+)\/\{z\}\/\{x\}\/\{y\}\.pbf$/)?.[1];
    return build && BUILD.test(build) ? [`${origin}/api/map/tiles/${build}/{z}/{x}/{y}.pbf`] : [];
  });
  if (tiles.length === 0) return Response.json({ error: "지도 타일 정보를 읽지 못했습니다." }, { status: 502 });
  return Response.json({ ...body, tiles }, { headers: { "cache-control": "private, max-age=3600" } });
}
