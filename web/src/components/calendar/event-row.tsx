"use client";

import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { timeLabel, type Ghost } from "@/lib/proposed";
import type { CalendarEvent, Decision } from "@/lib/thread";
import { cn } from "@/lib/utils";

import { OWNER_LABEL, OwnerBar, ownerOf, ownerText } from "@/components/calendar/owner";

const md = (date: string) => `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;

/** A settled entry. The calendar is shared with a partner, so every row says
 *  whose it is twice over: the colored edge to scan by, and the word to be
 *  sure. Who *wrote* it is said only when it is not who you would guess - a
 *  shared plan the partner put in.
 *
 *  A partner's entry has no delete button. Removing something from someone
 *  else's day is a conversation, not a tap. */
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
  const owner = ownerOf(event);
  const rest = meta(event);

  return (
    <div
      className={cn(
        "group/row flex items-start gap-3 rounded-lg py-2 pr-1 pl-2 transition-colors",
        "hover:bg-glass focus-within:bg-glass",
        leaving && "opacity-45",
      )}
    >
      <OwnerBar owner={owner} className="my-0.5" />
      <span className="tnum w-[3.6rem] shrink-0 pt-px text-[12px] leading-snug text-dim sm:w-[4.25rem]">
        {timeLabel(event)}
      </span>
      <span className="min-w-0 flex-1 text-[14.5px] leading-snug text-foreground/90">
        <span className={cn(leaving && "line-through decoration-reject/60")}>
          {event.title}
        </span>
        {event.place ? (
          <span className="text-dim"> · {event.place}</span>
        ) : null}
        <span className="mt-0.5 block text-[11.5px] leading-tight text-faint">
          <span className={cn("font-medium", ownerText[owner])}>{OWNER_LABEL[owner]}</span>
          {rest ? ` · ${rest}` : null}
        </span>
        {event.memo ? (
          <span className="mt-1 line-clamp-2 block text-[12.5px] leading-normal whitespace-pre-line text-dim">
            {event.memo}
          </span>
        ) : null}
      </span>
      {/* Hidden until pointed at is hidden forever on a touch screen, so
          `touch-visible` brings it back wherever hover does not exist. */}
      {owner === "partner" ? null : (
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onDelete}
        aria-label={`${event.title} 삭제`}
        className="tap touch-visible -my-2 shrink-0 text-faint opacity-0 transition-opacity group-hover/row:opacity-100 hover:bg-reject/10 hover:text-reject focus-visible:opacity-100 sm:my-0 sm:size-6 sm:min-h-0 sm:min-w-0"
      >
        <Trash2 aria-hidden className="size-4 sm:size-3.5" />
      </Button>
      )}
    </div>
  );
}

function meta(e: CalendarEvent): string {
  const parts: string[] = [];
  if (e.source === "partner" && ownerOf(e) !== "partner") parts.push("상대가 등록");
  if (e.endDate) parts.push(`${md(e.date)}–${md(e.endDate)}`);
  if (e.recurrence) parts.push("반복");
  return parts.join(" · ");
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
          <span className="tnum w-[3.6rem] shrink-0 text-[12px] text-dim sm:w-[4.25rem]">
            {timeLabel(preview)}
          </span>
          <span className="min-w-0 flex-1 text-[14.5px] leading-snug text-foreground/90">
            {preview.title}
            {preview.place ? <span className="text-dim"> · {preview.place}</span> : null}
          </span>
        </div>
      ) : null}

      <p className="mt-1.5 text-[12px] leading-normal text-dim">{copy.consequence}</p>

      {/* The same decision as the chat's card, so it gets the same treatment
          on a phone: full width, real height, a gap you cannot thumb across. */}
      <div className="mt-2.5 grid grid-cols-2 gap-2 sm:mt-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onDecide("rejected")}
          className="h-11 text-[13px] text-dim hover:bg-reject/10 hover:text-reject sm:h-7 sm:px-2.5 sm:text-[12.5px]"
        >
          반려
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => onDecide("approved")}
          className="h-11 border-edge bg-glass-raised text-[13px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve sm:h-7 sm:px-3 sm:text-[12.5px] dark:bg-glass-raised dark:hover:bg-approve/12"
        >
          승인
        </Button>
      </div>
    </div>
  );
}
