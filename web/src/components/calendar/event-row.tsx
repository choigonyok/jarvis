"use client";

import { useState, type ReactNode } from "react";
import { Trash2 } from "lucide-react";
import { Sending, Sent } from "@/components/chat/hold";
import { RejectForm } from "@/components/chat/reject-form";
import { Button } from "@/components/ui/button";
import { sentOn } from "@/lib/decide";
import { timeLabel, type Ghost } from "@/lib/proposed";
import type { CalendarEvent, Decision } from "@/lib/thread";
import { dayRange } from "@/lib/when";
import { cn } from "@/lib/utils";

import { OWNER_LABEL, OwnerBar, ownerOf, ownerText } from "@/components/calendar/owner";

/** A settled entry. The calendar is shared with a partner, so every row says
 *  whose it is twice over: the colored edge to scan by, and the word to be
 *  sure. Who *wrote* it is said only when it is not who you would guess - a
 *  shared plan the partner put in.
 *
 *  A partner's entry has no delete button, and does not open to be changed.
 *  Removing or changing something in someone else's day is a conversation,
 *  not a tap. One of your own opens, on a tap, to be changed in place. */
export function EventRow({
  event,
  pending,
  onEdit,
  onDelete,
}: {
  event: CalendarEvent;
  pending?: Ghost;
  /** Set where the entry may be changed here (calendar/event-sheet.tsx). */
  onEdit?: () => void;
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
      <Body onEdit={onEdit}>
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
      </Body>
      {/* Hidden until pointed at is hidden forever on a touch screen, so
          `touch-visible` brings it back wherever hover does not exist. */}
      {owner === "partner" ? null : (
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onDelete}
        aria-label={`${event.title} 삭제`}
        className="tap touch-visible -my-2 shrink-0 text-faint opacity-0 transition-opacity group-hover/row:opacity-100 hover:bg-reject/10 hover:text-reject focus-visible:opacity-100 mouse:my-0 mouse:size-6 mouse:min-h-0 mouse:min-w-0"
      >
        <Trash2 aria-hidden className="size-4 mouse:size-3.5" />
      </Button>
      )}
    </div>
  );
}

/**
 * The time and the words: a button that opens the entry where it may be
 * changed, else just themselves. Read out as the entry it is, then 고치기.
 */
function Body({ onEdit, children }: { onEdit?: () => void; children: ReactNode }) {
  if (!onEdit) return <>{children}</>;
  return (
    <button
      type="button"
      onClick={onEdit}
      className="-my-1 flex min-w-0 flex-1 items-start gap-3 rounded-md py-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {children}
      <span className="sr-only">고치기</span>
    </button>
  );
}

function meta(e: CalendarEvent): string {
  const parts: string[] = [];
  if (e.source === "partner" && ownerOf(e) !== "partner") parts.push("상대가 등록");
  if (e.endDate) parts.push(dayRange(e.date, e.endDate));
  if (e.recurrence) parts.push("반복");
  return parts.join(" · ");
}

const ghostCopy: Record<Ghost["op"], { label: string; consequence: string }> = {
  create: { label: "추가 제안", consequence: "승인하면 캘린더에 들어와요." },
  update: { label: "수정 제안", consequence: "승인하면 이 내용으로 바뀌어요." },
  delete: { label: "삭제 제안", consequence: "승인하면 지워져요. 되돌릴 수 없어요." },
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
  /** Resolves to what to say when it did not go. */
  onDecide: (decision: Decision, reason?: string) => Promise<string | null>;
}) {
  const copy = ghostCopy[ghost.op];
  const preview = ghost.op === "delete" ? null : ghost.preview;
  const [asking, setAsking] = useState(false);
  // As on the conversation's card: on its way, then taken and not back yet
  // (lib/decide.ts) - never the pair again while it is.
  const [busy, setBusy] = useState(false);
  const [sentAs, setSentAs] = useState(() => sentOn(ghost.proposal.id));
  async function decide(d: Decision, why?: string) {
    setBusy(true);
    const failed = await onDecide(d, why);
    setBusy(false);
    if (!failed) setSentAs(sentOn(ghost.proposal.id));
  }

  return (
    <div className="ml-2 rounded-lg border border-dashed border-edge bg-glass/60 px-3 py-2.5">
      <div className="flex items-center gap-2 text-[11px] text-faint">
        <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
        <span>{busy ? "보내는 중" : sentAs ? "반영 중" : copy.label}</span>
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
          on a phone: full width, real height, a gap you cannot thumb across -
          and turned down, the same question why. */}
      {busy ? (
        <Sending className="mt-1" />
      ) : sentAs ? (
        <Sent decision={sentAs} className="mt-1" />
      ) : asking ? (
        <div className="mt-2.5">
          <RejectForm onCancel={() => setAsking(false)} onReject={(why) => void decide("rejected", why)} />
        </div>
      ) : (
        <div className="mt-2.5 grid grid-cols-2 gap-2 sm:mt-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5">
          <Button
            variant="reject"
            size="pair-sm"
            onClick={() => setAsking(true)}
          >
            반려
          </Button>
          <Button
            variant="approve"
            size="pair-sm"
            onClick={() => void decide("approved")}
          >
            승인
          </Button>
        </div>
      )}
    </div>
  );
}
