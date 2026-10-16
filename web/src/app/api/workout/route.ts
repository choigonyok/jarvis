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
import { forwardWorkout as forward } from "@/lib/workout-proxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  return forward(request, "GET");
}

export async function PUT(request: Request) {
  return forward(request, "PUT", await request.text());
}

/**
 * The tab-close flush.
 *
 * `navigator.sendBeacon` can only issue POST. A beacon is treated as a set of
 * changes, never a full replace: it carries no revision, and a replace without
 * one is exactly how one writer used to wipe another's sessions.
 */
export async function POST(request: Request) {
  return forward(request, "POST", await request.text(), "/workout/changes");
}
