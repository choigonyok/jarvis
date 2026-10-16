// The /api/workout routes' way to workout-svc. On Cloudflare there is no
// compose network to reach, so WORKOUT_URL points at whatever is published in
// front of the service.
import { forward } from "@/lib/proxy";

const WORKOUT_URL = (process.env.WORKOUT_URL ?? "http://localhost:8091").replace(/\/$/, "");

export function forwardWorkout(
  request: Request,
  method: "GET" | "PUT" | "POST",
  body?: BodyInit,
  path = "/workout",
): Promise<Response> {
  return forward(
    request,
    WORKOUT_URL + path,
    { label: "운동 기록 서비스", requestHeaders: ["accept", "content-type", "if-match"], responseHeaders: ["etag"] },
    { method, body: body ?? null },
  );
}
