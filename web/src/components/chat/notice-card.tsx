"use client";

import { useState } from "react";
import { BellRing } from "lucide-react";
import { ChecklistBody, type Drop, type Fold, type Tick } from "@/components/chat/checklist";
import { Markdown } from "@/components/chat/markdown";
import type { Checklist, Turn } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * What was told at its moment - a reminder on coming home, the rain at the
 * door - laid in the conversation as well as pushed to the phone, so it is
 * there to come back to, and Jarvis knows it was said.
 *
 * Lit like a checklist, not framed like a decision: nothing waits on it.
 * Above its words, why now, the way the push said it. A reminder that
 * carries a list shows the list under them, ticked as it stands.
 */
export function NoticeCard({
  turn,
  list,
  onTick,
  onDrop,
  fold,
}: {
  turn: Turn;
  list?: Checklist;
  onTick: Tick;
  onDrop?: Drop;
  /** The list under it folds by its title, as a checklist card does. */
  fold?: Fold;
}) {
  const [said, ...more] = turn.paragraphs ?? [];
  const body = more.join("\n\n");
  const [open, setOpen] = useState(false);
  const long = body.length > 160 || body.split("\n").length > 3;
  const why = turn.origin?.title;
  return (
    <section className="w-full max-w-[34rem] rounded-xl bg-glass-raised px-4 py-3.5 sm:px-5">
      {why ? (
        <p className="flex items-center gap-1.5 text-[11.5px] text-faint" data-search-skip>
          <BellRing aria-hidden className="size-3.5 shrink-0" strokeWidth={1.8} />
          <span className="min-w-0">{why}</span>
        </p>
      ) : null}
      {said ? <h3 className={cn("text-[15px] leading-snug font-medium text-foreground", why && "mt-1.5")}>{said}</h3> : null}
      {body ? (
        <div className={cn("mt-1 text-[13.5px] leading-relaxed text-dim [&_p]:my-0 [&_ul]:my-1", !open && long && "line-clamp-4")}>
          <Markdown>{body}</Markdown>
        </div>
      ) : null}
      {body && long ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="tap mt-1 text-[12px] text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
          data-search-skip
        >
          {open ? "접기" : "더 보기"}
        </button>
      ) : null}
      {turn.origin?.checklist ? (
        <div className="-mb-1.5 mt-3 border-t border-edge-soft pt-3">
          <ChecklistBody list={list} onTick={onTick} onDrop={onDrop} fold={fold} />
        </div>
      ) : null}
    </section>
  );
}
