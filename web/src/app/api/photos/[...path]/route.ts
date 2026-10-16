import { pathProxy } from "@/lib/proxy";

// Immich, through the gateway (which holds Immich's API key and only lets
// reads and searches through). Thumbnails, originals and video stream
// straight through, Range requests included, so a video can be scrubbed.
const PHOTOS_URL = process.env.PHOTOS_URL ?? "http://localhost:2283";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(PHOTOS_URL, {
  label: "사진 서버",
  requestHeaders: ["range", "accept", "content-type", "if-none-match"],
  responseHeaders: ["content-length", "content-range", "accept-ranges", "etag", "content-disposition"],
});

export const GET = proxy;
export const POST = proxy;
