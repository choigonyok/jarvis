"use client";

import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { timeLabel, type Ghost } from "@/lib/proposed";
import type { CalendarEvent, Decision } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** A settled entry. Nothing marks who put it here: an approved change is the
 *  operator's change, the same as one they typed. */
export function EventRow({
  event,
  pending,
  onDelete,
}: {
  event: CalendarEvent;
  pending?: Ghost;
  onDelete: () => void;
}) {
  const leaving = pending?.op === "delete";

  return (
    <div
      className={cn(
        "group/row flex items-baseline gap-3 rounded-lg py-1.5 pr-1 pl-2 transition-colors",
        "hover:bg-glass focus-within:bg-glass",
        leaving && "opacity-45",
      )}
    >
      <span className="tnum w-[4.25rem] shrink-0 text-[12px] text-dim">
        {timeLabel(event)}
      </span>
      <span className="min-w-0 flex-1 text-[14.5px] leading-snug text-foreground/90">
        <span className={cn(leaving && "line-through decoration-reject/60")}>
          {event.title}
        </span>
        {event.place ? (
          <span className="text-dim"> · {event.place}</span>
        ) : null}
      </span>
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onDelete}
        aria-label={`${event.title} 삭제`}
        className="shrink-0 text-faint opacity-0 transition-opacity group-hover/row:opacity-100 hover:bg-reject/10 hover:text-reject focus-visible:opacity-100"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </Button>
    </div>
  );
}

const ghostCopy: Record<Ghost["op"], { label: string; consequence: string }> = {
  create: { label: "추가 제안", consequence: "승인하면 캘린더에 들어옵니다." },
  update: { label: "수정 제안", consequence: "승인하면 이 내용으로 바뀝니다." },
  delete: { label: "삭제 제안", consequence: "승인하면 지워집니다. 되돌릴 수 없습니다." },
};

/**
 * An undecided change, sitting where it would land. A dashed edge and the
 * breathing dot say it is not real yet; approving settles it into place.
 */
export function GhostRow({
  ghost,
  onDecide,
}: {
  ghost: Ghost;
  onDecide: (decision: Decision) => void;
}) {
  const copy = ghostCopy[ghost.op];
  const preview = ghost.op === "delete" ? null : ghost.preview;

  return (
    <div className="ml-2 rounded-lg border border-dashed border-edge bg-glass/60 px-3 py-2.5 backdrop-blur-md">
      <div className="flex items-center gap-2 text-[11px] text-faint">
        <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
        <span>{copy.label}</span>
      </div>

      {preview ? (
        <div className="mt-1.5 flex items-baseline gap-3">
          <span className="tnum w-[4.25rem] shrink-0 text-[12px] text-dim">
            {timeLabel(preview)}
          </span>
          <span className="min-w-0 flex-1 text-[14.5px] leading-snug text-foreground/90">
            {preview.title}
            {preview.place ? <span className="text-dim"> · {preview.place}</span> : null}
          </span>
        </div>
      ) : null}

      <p className="mt-1.5 text-[12px] leading-normal text-dim">{copy.consequence}</p>

      <div className="mt-2 flex items-center justify-end gap-1.5">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onDecide("rejected")}
          className="h-7 px-2.5 text-[12.5px] text-dim hover:bg-reject/10 hover:text-reject"
        >
          반려
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => onDecide("approved")}
          className="h-7 border-edge bg-glass-raised px-3 text-[12.5px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve dark:bg-glass-raised dark:hover:bg-approve/12"
        >
          승인
        </Button>
      </div>
    </div>
  );
}
