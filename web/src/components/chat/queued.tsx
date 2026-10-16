"use client";

import { X } from "lucide-react";
import type { Queued } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * Messages sent while Jarvis was busy. Each is drawn as the bubble it will
 * become, but in outline only: written, not yet read. When the running turn
 * takes it - steered in, or its turn comes - it leaves this list and shows up
 * in the transcript as a solid bubble.
 */
export function QueuedMessages({
  queue,
  onSteer,
  onDrop,
}: {
  queue: Queued[];
  onSteer: (id: string) => void;
  onDrop: (id: string) => void;
}) {
  if (queue.length === 0) return null;
  return (
    <ol
      aria-label="보내기를 기다리는 메시지"
      className="scrollbar-hairline mb-2 flex max-h-[38vh] flex-col items-end gap-2 overflow-y-auto overscroll-contain pt-1"
    >
      {queue.map((q) => (
        <li key={q.id} className="flex max-w-[85%] flex-col items-end gap-0.5 sm:max-w-[26rem]">
          <p
            className={cn(
              "rounded-2xl border border-dashed border-edge px-4 py-2 text-[14px] leading-relaxed text-dim",
              "line-clamp-3 break-words whitespace-pre-wrap",
            )}
          >
            {q.text || (q.images?.length ? `사진 ${q.images.length}장` : "")}
          </p>
          <span className="flex items-center">
            {/* Steer: the running turn reads it at its next step instead of
                after it ends. The one action that matters here, so it is the
                one with words. */}
            <button
              type="button"
              onClick={() => onSteer(q.id)}
              aria-label="진행 중인 작업에 바로 반영"
              className="tap flex h-11 items-center rounded-full px-3 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8"
            >
              바로 반영
            </button>
            <button
              type="button"
              onClick={() => onDrop(q.id)}
              aria-label="보내지 않고 빼기"
              className="tap flex size-11 items-center justify-center rounded-full text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 sm:size-8"
            >
              <X aria-hidden className="size-4" />
            </button>
          </span>
        </li>
      ))}
    </ol>
  );
}
