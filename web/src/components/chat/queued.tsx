"use client";

import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { Accordion } from "radix-ui";
import type { Queued } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** How long "보내지 않았어요 · 되돌리기" stays before it goes. */
const UNDO_MS = 6000;

type Dropped = Queued & { droppedAt: number };

// The composer's own columns: the leading control (or its padding), the text
// where typing starts, then the controls - ✕ over the send button. A queued
// line reads as part of the field it was typed in.
const ROW =
  "grid grid-cols-[3rem_minmax(0,1fr)_auto_3rem] items-start group-data-[lead=text]/composer:grid-cols-[0.875rem_minmax(0,1fr)_auto_3rem]";
const TEXT = "pl-1 group-data-[lead=text]/composer:pl-0";

/**
 * What was sent while Jarvis was working, at the top of the composer's own
 * glass - the outbox above the line being written, the way a quoted message
 * sits there too. One line each, oldest first (the order they will go in); a
 * tap shows the whole message. 지금 반영 hands one to the running turn, which
 * reads it at its next step; ✕ takes it back, with a moment to undo.
 *
 * One message is open at a time, and the arrow keys go up and down the lines:
 * an accordion (Radix's own, for its state and keys - the line itself is what
 * opens, in place, so there is no panel under it).
 */
export function QueuedMessages({
  queue,
  onSteer,
  onDrop,
  onRestore,
}: {
  queue: Queued[];
  onSteer: (id: string) => void | Promise<void>;
  onDrop: (id: string) => void | Promise<void>;
  /** Sends a dropped message again - the undo of ✕. */
  onRestore?: (text: string, images?: string[]) => void;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const [steering, setSteering] = useState<string | null>(null);
  const [dropped, setDropped] = useState<Dropped[]>([]);

  // An undo is offered for a while, then the line goes.
  useEffect(() => {
    if (dropped.length === 0) return;
    const next = Math.min(...dropped.map((d) => d.droppedAt)) + UNDO_MS - Date.now();
    const t = window.setTimeout(
      () => setDropped((cur) => cur.filter((d) => Date.now() - d.droppedAt < UNDO_MS)),
      Math.max(0, next),
    );
    return () => window.clearTimeout(t);
  }, [dropped]);

  const rows = [
    ...queue.map((q) => ({ q, gone: false })),
    ...dropped.filter((d) => !queue.some((q) => q.id === d.id)).map((q) => ({ q, gone: true })),
  ].sort((a, b) => a.q.at.localeCompare(b.q.at));
  if (rows.length === 0) return null;

  return (
    <Accordion.Root
      type="single"
      collapsible
      value={open ?? ""}
      onValueChange={(v) => setOpen(v || null)}
      asChild
    >
    <ol
      aria-label={`보내기를 기다리는 메시지 ${queue.length}개`}
      className="scrollbar-hairline mb-1 max-h-[11.5rem] overflow-y-auto overscroll-contain border-b border-edge-soft pb-1"
    >
      {rows.map(({ q, gone }) => {
        const text = q.text || (q.images?.length ? `사진 ${q.images.length}장` : "");

        if (gone) {
          // The controls give way to what happened, as on a decided card.
          return (
            <li key={q.id} className={cn(ROW, "min-h-11 items-center mouse:min-h-10")} role="status">
              <span className={cn(TEXT, "col-start-2 truncate text-ui text-dim")}>보내지 않았어요</span>
              {onRestore ? (
                <button
                  type="button"
                  onClick={() => {
                    setDropped((cur) => cur.filter((d) => d.id !== q.id));
                    onRestore(q.text, q.images);
                  }}
                  className="tap col-span-2 col-start-3 flex h-11 items-center justify-self-end rounded-full px-3 text-aside text-ink hover:text-foreground focus-ring mouse:h-10"
                >
                  되돌리기
                </button>
              ) : null}
            </li>
          );
        }

        const busy = steering === q.id;
        return (
          <Accordion.Item key={q.id} value={q.id} asChild>
          <li className={ROW}>
            {/* The system's mark for "not yet": the same breathing dot as a
                calendar entry waiting on a decision, centred under the photo
                button (or in the padding where there is none). */}
            <span aria-hidden className="anim-breathe mt-[19.5px] size-[5px] justify-self-center rounded-full bg-dim sm:mt-[17.5px]" />
            <Accordion.Header asChild>
              <div className="min-w-0">
                <Accordion.Trigger
                  title={open === q.id ? undefined : text}
                  className={cn(
                    TEXT,
                    "block min-h-11 w-full min-w-0 truncate py-3 text-left text-ui leading-[1.45] text-dim outline-none mouse:min-h-10 mouse:py-2.5",
                    "rounded-md focus-ring",
                    "data-[state=open]:overflow-visible data-[state=open]:break-words data-[state=open]:whitespace-pre-wrap data-[state=open]:text-ink",
                  )}
                >
                  {text}
                </Accordion.Trigger>
              </div>
            </Accordion.Header>
            {/* The send button's quiet glass: the brightest control in this
                product stays 승인. A thumb high, the glass inside smaller. */}
            <button
              type="button"
              disabled={busy}
              onClick={async () => {
                setSteering(q.id);
                try {
                  await onSteer(q.id);
                } finally {
                  setSteering(null);
                }
              }}
              aria-label={busy ? undefined : "진행 중인 작업에 지금 반영"}
              className="tap group ml-2 flex h-11 items-center rounded-full outline-none mouse:h-10"
            >
              <span className="flex h-8 items-center rounded-full bg-glass-raised px-3 text-aside whitespace-nowrap text-dim transition-colors group-hover:text-foreground group-focus-visible:ring-3 group-focus-visible:ring-ring/50 group-disabled:opacity-60 mouse:h-7">
                {busy ? "반영하는 중" : "지금 반영"}
              </span>
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => {
                if (open === q.id) setOpen(null);
                setDropped((cur) => [...cur, { ...q, droppedAt: Date.now() }]);
                void onDrop(q.id);
              }}
              aria-label="보내지 않고 빼기"
              className="tap flex size-11 items-center justify-center justify-self-center rounded-full text-faint hover:text-foreground focus-ring disabled:opacity-40 mouse:size-10"
            >
              <X aria-hidden className="size-4" />
            </button>
          </li>
          </Accordion.Item>
        );
      })}
    </ol>
    </Accordion.Root>
  );
}
