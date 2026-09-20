import type { CalendarEvent, Proposal } from "@/lib/thread";

/**
 * A pending calendar proposal, resolved against the calendar it would change.
 *
 * The point of this file: an undecided change is shown in its place on the
 * calendar rather than in a separate queue, so the operator decides while
 * looking at what the day would become.
 */
export type Ghost =
  | { op: "create"; proposal: Proposal; preview: PreviewEvent }
  | { op: "update"; proposal: Proposal; targetId: string; preview: PreviewEvent }
  | { op: "delete"; proposal: Proposal; targetId: string };

/** A ghost that needs a place of its own in a day. */
export type CreateGhost = Extract<Ghost, { op: "create" }>;

export type PreviewEvent = {
  date: string;
  start?: string;
  end?: string;
  title: string;
  place?: string;
};

function str(input: Record<string, unknown>, key: string): string | undefined {
  const value = input[key];
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

/**
 * Reads one proposal's tool arguments. Anything malformed is dropped rather
 * than rendered: a ghost we cannot place honestly is worse than none, and the
 * card in the chat still carries the full text either way.
 */
export function toGhost(proposal: Proposal, events: CalendarEvent[]): Ghost | null {
  const input = proposal.action.input ?? {};

  switch (proposal.action.kind) {
    case "calendar.create_event": {
      const date = str(input, "date");
      const title = str(input, "title");
      if (!date || !title) return null;
      return {
        op: "create",
        proposal,
        preview: {
          date,
          title,
          start: str(input, "start"),
          end: str(input, "end"),
          place: str(input, "place"),
        },
      };
    }
    case "calendar.update_event": {
      const targetId = str(input, "id");
      const target = events.find((e) => e.id === targetId);
      if (!targetId || !target) return null;
      return {
        op: "update",
        proposal,
        targetId,
        // 보내지 않은 필드는 그대로 둔다 - 서버의 패치 규칙과 같다.
        preview: {
          date: str(input, "date") ?? target.date,
          title: str(input, "title") ?? target.title,
          start: str(input, "start") ?? target.start,
          end: str(input, "end") ?? target.end,
          place: str(input, "place") ?? target.place,
        },
      };
    }
    case "calendar.delete_event": {
      const targetId = str(input, "id");
      if (!targetId || !events.some((e) => e.id === targetId)) return null;
      return { op: "delete", proposal, targetId };
    }
    default:
      return null;
  }
}

export function ghostsFor(proposals: Proposal[], events: CalendarEvent[]): Ghost[] {
  return proposals
    .map((p) => toGhost(p, events))
    .filter((g): g is Ghost => g !== null);
}

/** "19:00–22:00", "19:00", or "종일". */
export function timeLabel(e: { start?: string; end?: string }): string {
  if (!e.start) return "종일";
  return e.end ? `${e.start}–${e.end}` : e.start;
}
