"use client";

import { useEffect, useRef, useState } from "react";
import type { Checklist, ChecklistItem } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** Ticks or unticks an item; resolves to what to say when it was not saved. */
export type Tick = (itemId: string, done: boolean) => Promise<string | null>;

/**
 * A list to tick through - what to pack, what to do on getting home -
 * read down on a phone at the door, one thumb on it.
 *
 * Every row is the button: 44px tall, the whole width, so a tap anywhere on
 * the words ticks it. Nothing moves when ticked - a row sliding away under
 * the thumb is how the next tap lands on the wrong one - it only goes quiet.
 * What is ticked is saved (mind keeps it), so the list reads the same the
 * next time it is shown, on any screen.
 */
export function ChecklistBody({ list, title, onTick }: { list?: Checklist; title?: string; onTick: Tick }) {
  const items = list?.items ?? [];
  const done = items.filter((i) => i.done).length;
  const all = items.length > 0 && done === items.length;
  // The one moment a list has: finishing it here, not opening it finished.
  const was = useRef(all);
  const [finished, setFinished] = useState(false);
  useEffect(() => {
    // Not a cascade: it notes, once, the tap that finished the list.
    if (all && !was.current) setFinished(true);
    was.current = all;
  }, [all]);
  // Items ticked on this screen draw their mark in; ones ticked before just are.
  const [fresh, setFresh] = useState<ReadonlySet<string>>(() => new Set());
  const [refusal, setRefusal] = useState<string | null>(null);

  if (!list) {
    return (
      <div>
        <h3 className="text-[14.5px] leading-snug font-medium text-foreground">{title || "체크리스트"}</h3>
        <p className="mt-1 text-[12.5px] text-faint">목록을 불러오지 못했어요. 새로고침하면 다시 읽어 와요.</p>
      </div>
    );
  }

  async function toggle(item: ChecklistItem) {
    setRefusal(null);
    const next = !item.done;
    if (next) setFresh((cur) => new Set(cur).add(item.id));
    const said = await onTick(item.id, next);
    if (said) setRefusal(said);
  }

  const share = items.length ? (done / items.length) * 100 : 0;
  return (
    <div data-checklist-id={list.id}>
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="min-w-0 text-[14.5px] leading-snug font-medium text-foreground">{list.title}</h3>
        <span className={cn("tnum shrink-0 text-[12px]", all ? "text-approve" : "text-faint")} aria-live="polite">
          {all ? "다 했어요" : `${done}/${items.length}`}
        </span>
      </div>
      {list.note || list.status === "archived" ? (
        <p className="mt-0.5 text-[12px] leading-normal text-faint">
          {[list.note, list.status === "archived" ? "닫은 목록이에요" : ""].filter(Boolean).join(" - ")}
        </p>
      ) : null}
      <div aria-hidden className="mt-2.5 h-[2px] overflow-hidden rounded-full bg-edge">
        <div
          className={cn(
            "h-full rounded-full transition-[width,background-color] duration-300 ease-out motion-reduce:transition-none",
            all ? "bg-approve" : "bg-foreground/60",
            finished && "list-done",
          )}
          style={{ width: `${share}%` }}
        />
      </div>
      <ul className="-mx-2 mt-1.5" aria-label={list.title}>
        {items.map((item) => (
          <li key={item.id}>
            <button
              type="button"
              role="checkbox"
              aria-checked={item.done}
              onClick={() => void toggle(item)}
              className="group flex min-h-11 w-full items-start gap-3 rounded-lg px-2 py-2.5 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-9 mouse:py-2"
            >
              <Mark done={item.done} draw={fresh.has(item.id)} />
              <span
                className={cn(
                  "min-w-0 flex-1 text-[14px] leading-snug transition-colors duration-200 motion-reduce:transition-none",
                  item.done ? "text-faint line-through decoration-faint/70" : "text-foreground",
                )}
              >
                {item.text}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {refusal ? (
        <p role="alert" className="mt-1 px-0.5 text-[12px] text-reject">
          {refusal}
        </p>
      ) : null}
    </div>
  );
}

/** The ring an item is ticked in; filled, with the mark drawn, once done. */
function Mark({ done, draw }: { done: boolean; draw: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "mt-px flex size-5 shrink-0 items-center justify-center rounded-full border-[1.5px] transition-colors duration-150 motion-reduce:transition-none",
        done ? "border-foreground bg-foreground" : "border-dim group-hover:border-foreground/80",
      )}
    >
      {done ? (
        <svg viewBox="0 0 12 12" className="size-3">
          <path
            d="M2.6 6.3 5 8.6 9.5 3.7"
            fill="none"
            stroke="var(--background)"
            strokeWidth="1.9"
            strokeLinecap="round"
            strokeLinejoin="round"
            className={draw ? "tick-draw" : undefined}
          />
        </svg>
      ) : null}
    </span>
  );
}

/**
 * A checklist shown in the conversation on its own: lit, not framed - a
 * frame is for a decision, and nothing here waits on one.
 */
export function ChecklistCard({ list, title, onTick }: { list?: Checklist; title?: string; onTick: Tick }) {
  return (
    <section className="w-full max-w-[34rem] rounded-xl bg-glass-raised px-4 pt-3.5 pb-2 sm:px-5">
      <ChecklistBody list={list} title={title} onTick={onTick} />
    </section>
  );
}
