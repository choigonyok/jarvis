import { forwardWorkout } from "@/lib/workout-proxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

/** Only what changed since the last save - sessions by id, deletions by id. */
export async function POST(request: Request) {
  return forwardWorkout(request, "POST", await request.text(), "/workout/changes");
}
