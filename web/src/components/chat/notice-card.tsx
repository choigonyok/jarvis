"use client";

import { useState } from "react";
import { BellRing } from "lucide-react";
import { ChecklistBody, type Drop, type Fold, type Tick } from "@/components/chat/checklist";
import { GoalAsks } from "@/components/chat/goal-asks";
import { Markdown } from "@/components/chat/markdown";
import { CopyButton } from "@/components/ui/copy-button";
import type { Checklist, Turn } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * What was told at its moment - a reminder on coming home, the rain at the
 * door - laid in the conversation as well as pushed to the phone, so it is
 * there to come back to, and Jarvis knows it was said.
 *
 * Lit like a checklist, not framed like a decision: nothing waits on it.
 * Above its words, why now, the way the push said it. A reminder that
 * carries a list shows the list under them, ticked as it stands; one that asks
 * about a goal's day takes the answer there.
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
  // What was only said: a list copies itself, and a goal's question is answered.
  const copied = !turn.origin?.checklist && !turn.origin?.goals?.length ? [said, body].filter(Boolean).join("\n") : "";
  return (
    <section className="relative w-full max-w-[34rem] rounded-xl bg-glass-raised px-4 py-3.5 sm:px-5">
      {copied ? <CopyButton text={copied} label="알림 복사" className="absolute top-0 right-0" /> : null}
      {why ? (
        <p className={cn("flex items-center gap-1.5 text-meta text-faint", copied && "pr-7")} data-search-skip>
          <BellRing aria-hidden className="size-3.5 shrink-0" strokeWidth={1.8} />
          <span className="min-w-0">{why}</span>
        </p>
      ) : null}
      {said ? <h3 className={cn("text-title leading-snug font-medium text-foreground", why && "mt-1.5", copied && !why && "pr-7")}>{said}</h3> : null}
      {body ? (
        <div className={cn("mt-1 text-ui leading-relaxed text-dim [&_p]:my-0 [&_ul]:my-1", !open && long && "line-clamp-4")}>
          <Markdown>{body}</Markdown>
        </div>
      ) : null}
      {body && long ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="tap mt-1 text-aside text-faint hover:text-dim focus-ring"
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
      {turn.origin?.goals?.length ? <GoalAsks asks={turn.origin.goals} said={said} /> : null}
    </section>
  );
}
