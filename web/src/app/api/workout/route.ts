// Server-side proxy to the training log, which input-svc keeps. The bearer
// token stays here, and this route itself sits behind the console login (see
// src/middleware.ts).
import { forwardWorkout as forward } from "@/lib/workout-proxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  return forward(request, "GET");
}

/**
 * The tab-close flush.
 *
 * `navigator.sendBeacon` can only issue POST. A beacon is a set of changes,
 * never a full replace: there is no full replace.
 */
export async function POST(request: Request) {
  return forward(request, "POST", await request.text(), "/workout/changes");
}
