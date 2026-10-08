// Immich (v3) through /api/photos. Shapes as Immich sends them.

export const PHOTOS = "/api/photos/api";

export type Bucket = { timeBucket: string; count: number };

/** One photo or video, flattened from Immich's columnar bucket. */
export type Asset = {
  id: string;
  isImage: boolean;
  /** A video's length: milliseconds in the timeline, "0:00:12.345000" in search. */
  duration: number | string | null;
  takenAt: string;
  ratio: number;
};

type Columns = {
  id: string[];
  isImage: boolean[];
  duration: (number | string | null)[];
  fileCreatedAt: string[];
  ratio: (number | null)[];
};

export function flatten(c: Columns): Asset[] {
  return c.id.map((id, i) => ({
    id,
    isImage: c.isImage[i],
    duration: c.duration[i],
    takenAt: c.fileCreatedAt[i],
    ratio: c.ratio[i] ?? 1,
  }));
}

export type Album = {
  id: string;
  albumName: string;
  assetCount: number;
  albumThumbnailAssetId: string | null;
};

export const thumbUrl = (id: string, size: "thumbnail" | "preview" = "thumbnail") =>
  `${PHOTOS}/assets/${id}/thumbnail?size=${size}`;
export const videoUrl = (id: string) => `${PHOTOS}/assets/${id}/video/playback`;
export const originalUrl = (id: string) => `${PHOTOS}/assets/${id}/original`;

/** 65120 (ms) or "0:01:05.120000" → "1:05". */
export function clip(duration: number | string | null): string {
  if (duration == null || duration === "") return "";
  let total: number;
  if (typeof duration === "number") total = Math.round(duration / 1000);
  else {
    const [h, m, s] = duration.split(":").map((x) => Number(x));
    total = Math.round(h * 3600 + m * 60 + s);
  }
  if (!Number.isFinite(total)) return "";
  const mm = Math.floor(total / 60);
  return `${mm}:${String(total % 60).padStart(2, "0")}`;
}

/** "2026-10-01" → "2026년 10월". */
export const monthLabel = (bucket: string) => {
  const [y, m] = bucket.split("-");
  return `${y}년 ${Number(m)}월`;
};

export async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${PHOTOS}/${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(String(res.status));
  return (await res.json()) as T;
}

export async function smartSearch(query: string): Promise<Asset[]> {
  const res = await fetch(`${PHOTOS}/search/smart`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ query, size: 120 }),
  });
  if (!res.ok) throw new Error(String(res.status));
  const body = (await res.json()) as {
    assets: { items: { id: string; type: string; duration: string | null; fileCreatedAt: string; exifInfo?: { exifImageWidth?: number; exifImageHeight?: number } }[] };
  };
  return body.assets.items.map((a) => ({
    id: a.id,
    isImage: a.type === "IMAGE",
    duration: a.type === "VIDEO" ? a.duration : null,
    takenAt: a.fileCreatedAt,
    ratio: 1,
  }));
}
