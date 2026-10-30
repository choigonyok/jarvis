// The /api/workout routes' way to the training log (input-svc). On Cloudflare
// there is no compose network to reach, so WORKOUT_URL points at the gateway.
import { forward } from "@/lib/proxy";

const WORKOUT_URL = (process.env.WORKOUT_URL ?? "http://localhost:8101").replace(/\/$/, "");

export function forwardWorkout(
  request: Request,
  method: "GET" | "POST",
  body?: BodyInit,
  path = "/workout",
): Promise<Response> {
  return forward(
    request,
    WORKOUT_URL + path,
    { label: "운동 기록", requestHeaders: ["accept", "content-type", "if-match"], responseHeaders: ["etag"] },
    { method, body: body ?? null },
  );
}
