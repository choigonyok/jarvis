import { API } from "@/lib/thread";

/** Where an attached photo is served from (the agent, through the proxy). */
export const photoUrl = (name: string) => `${API}/uploads/${encodeURIComponent(name)}`;

/** Longest edge after shrinking. Enough for a listing photo, far below a phone's 12MP. */
const MAX_EDGE = 2048;

/**
 * Re-encodes a photo as a JPEG no larger than MAX_EDGE on its long side.
 *
 * A phone photo is 3-8MB and often HEIC, which the agent does not accept and
 * the model cannot read; drawing it to a canvas turns either into a JPEG the
 * size of a web image. createImageBitmap applies the EXIF rotation, so a
 * portrait shot stays upright.
 */
export async function shrink(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height));
  const w = Math.round(bitmap.width * scale);
  const h = Math.round(bitmap.height * scale);
  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("사진을 줄이지 못했습니다.");
  ctx.drawImage(bitmap, 0, 0, w, h);
  bitmap.close();
  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.86));
  if (!blob) throw new Error("사진을 줄이지 못했습니다.");
  return blob;
}

/** Uploads one photo; resolves to the name the agent gave it. */
export async function uploadPhoto(file: File): Promise<string> {
  const body = await shrink(file);
  const res = await fetch(`${API}/uploads`, {
    method: "POST",
    headers: { "content-type": "image/jpeg" },
    body,
  });
  const json = (await res.json().catch(() => null)) as { name?: string; error?: string } | null;
  if (!res.ok || !json?.name) throw new Error(json?.error ?? "사진을 올리지 못했습니다.");
  return json.name;
}
