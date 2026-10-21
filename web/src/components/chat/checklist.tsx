"use client";

import { useEffect, useId, useRef, useState } from "react";
import { ChevronDown, X } from "lucide-react";
import { UNDO_MS } from "@/components/shell/undo";
import type { Checklist, ChecklistItem } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** Ticks or unticks an item; resolves to what to say when it was not saved. */
export type Tick = (itemId: string, done: boolean) => Promise<string | null>;
/** Takes an item off the list; resolves to what to say when it was not. */
export type Drop = (itemId: string) => Promise<string | null>;

/** How far a row is pulled before letting go strikes it off. */
const STRIKE = 72;

/** A list shown folded until asked: its title, how far along, what is left. */
export type Fold = { open: boolean; onToggle: () => void };

/**
 * A list to tick through - what to pack, what to do on getting home -
 * read down on a phone at the door, one thumb on it.
 *
 * Every row is the button: 44px tall, the whole width, so a tap anywhere on
 * the words ticks it. Ticked, a row goes quiet in place - nothing moves under
 * the thumb. One that is not needed is pulled right to left: the pull draws a
 * line through it from the right, and let go past the mark it is struck off,
 * with 되돌리기 on its row for five seconds before it leaves the list. A
 * strike-through means only that - taken off - never "done".
 *
 * What is ticked and taken off is kept by mind, so the list reads the same
 * the next time it is shown, wherever.
 */
export function ChecklistBody({
  list,
  title,
  onTick,
  onDrop,
  onClose,
  fold,
}: {
  list?: Checklist;
  title?: string;
  onTick: Tick;
  onDrop?: Drop;
  /** Set where a list may be closed from (the 작업 tab). */
  onClose?: () => Promise<string | null>;
  /** Set where lists stand folded, a few to a screen (the 작업 tab). */
  fold?: Fold;
}) {
  const itemsId = useId();
  const items = list?.items ?? [];
  // Struck off and waiting out their five seconds: still on screen, struck.
  const [striking, setStriking] = useState<ReadonlySet<string>>(() => new Set());
  const live = items.filter((i) => !striking.has(i.id));
  const done = live.filter((i) => i.done).length;
  const all = live.length > 0 && done === live.length;
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

  // Each struck item's five seconds. Leaving the screen is not an undo: what
  // is still waiting goes then, as the calendar's removals do.
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const dropRef = useRef(onDrop);
  useEffect(() => {
    dropRef.current = onDrop;
  }, [onDrop]);
  useEffect(() => {
    const pending = timers.current;
    return () => {
      for (const [id, t] of pending) {
        clearTimeout(t);
        void dropRef.current?.(id);
      }
      pending.clear();
    };
  }, []);

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

  function strike(item: ChecklistItem) {
    if (!onDrop || timers.current.has(item.id)) return;
    setRefusal(null);
    setStriking((cur) => new Set(cur).add(item.id));
    timers.current.set(
      item.id,
      setTimeout(async () => {
        timers.current.delete(item.id);
        const said = await dropRef.current?.(item.id);
        setStriking((cur) => {
          const next = new Set(cur);
          next.delete(item.id);
          return next;
        });
        if (said) setRefusal(said);
      }, UNDO_MS),
    );
  }

  function unstrike(item: ChecklistItem) {
    const t = timers.current.get(item.id);
    if (t) clearTimeout(t);
    timers.current.delete(item.id);
    setStriking((cur) => {
      const next = new Set(cur);
      next.delete(item.id);
      return next;
    });
  }

  const share = live.length ? (done / live.length) * 100 : 0;
  const count = (
    <span className={cn("tnum shrink-0 text-[12px]", all ? "text-approve" : "text-faint")} aria-live="polite">
      {all ? "다 했어요" : `${done}/${live.length}`}
    </span>
  );
  const open = !fold || fold.open;
  const left = live.filter((i) => !i.done);
  return (
    <div data-checklist-id={list.id}>
      {fold ? (
        // The accordion's own pattern: the heading holds the button.
        <h3 className="-mx-2 -mt-1.5">
          <button
            type="button"
            onClick={fold.onToggle}
            aria-expanded={fold.open}
            aria-controls={itemsId}
            className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-9"
          >
            <span className="min-w-0 text-[14.5px] leading-snug font-medium text-foreground">{list.title}</span>
            <span className="flex shrink-0 items-center gap-1.5">
              {count}
              <ChevronDown
                aria-hidden
                className={cn("size-4 text-faint transition-transform duration-200 motion-reduce:transition-none", fold.open && "rotate-180")}
              />
            </span>
          </button>
        </h3>
      ) : (
        <div className="flex items-baseline justify-between gap-3">
          <h3 className="min-w-0 text-[14.5px] leading-snug font-medium text-foreground">{list.title}</h3>
          {count}
        </div>
      )}
      {list.note || list.status === "archived" ? (
        <p className="mt-0.5 text-[12px] leading-normal text-faint">
          {[list.note, list.status === "archived" ? "닫은 목록이에요" : ""].filter(Boolean).join(" - ")}
        </p>
      ) : null}
      <div aria-hidden className={cn("h-[2px] overflow-hidden rounded-full bg-edge", fold ? "mt-1.5" : "mt-2.5")}>
        <div
          className={cn(
            "h-full rounded-full transition-[width,background-color] duration-300 ease-out motion-reduce:transition-none",
            all ? "bg-approve" : "bg-foreground/60",
            finished && "list-done",
          )}
          style={{ width: `${share}%` }}
        />
      </div>
      {open ? (
        <div id={itemsId}>
          <ul className="-mx-2 mt-1.5" aria-label={list.title}>
            {items.map((item) => (
              <Row
                key={item.id}
                item={item}
                draw={fresh.has(item.id)}
                struck={striking.has(item.id)}
                onToggle={() => void toggle(item)}
                onStrike={onDrop ? () => strike(item) : undefined}
                onUnstrike={() => unstrike(item)}
              />
            ))}
          </ul>
          {refusal ? (
            <p role="alert" className="mt-1 px-0.5 text-[12px] text-reject">
              {refusal}
            </p>
          ) : null}
          {onClose ? <CloseList onClose={onClose} /> : null}
        </div>
      ) : left.length > 0 ? (
        // Folded: what is left, in a line - enough to know whether to open it.
        <p id={itemsId} className="mt-2 truncate text-[12.5px] text-dim">
          남은 것: {left.slice(0, 4).map((i) => i.text).join(", ")}
          {left.length > 4 ? ` 외 ${left.length - 4}개` : ""}
        </p>
      ) : null}
    </div>
  );
}

/**
 * One item. A tap ticks it; a pull to the left strikes it off. Only a
 * sideways pull on a touch screen is taken (a vertical move scrolls), and the
 * row is marked data-no-swipe so the conversation's own pull - quoting the
 * turn - does not start under it.
 */
function Row({
  item,
  draw,
  struck,
  onToggle,
  onStrike,
  onUnstrike,
}: {
  item: ChecklistItem;
  draw: boolean;
  struck: boolean;
  onToggle: () => void;
  onStrike?: () => void;
  onUnstrike: () => void;
}) {
  const line = useRef<HTMLSpanElement>(null);
  const hint = useRef<HTMLSpanElement>(null);
  const pull = useRef<{ x: number; y: number; sideways: boolean | null; past: boolean } | null>(null);
  // A pull ends in a pointerup that would also be a click on the row.
  const swallow = useRef(false);

  function paint(progress: number) {
    // The scale property, not transform: Tailwind's scale-x-0 sets that one,
    // and the two would multiply.
    if (line.current) line.current.style.scale = `${progress} 1`;
    if (hint.current) hint.current.style.opacity = String(Math.min(1, progress * 1.4));
  }

  function release(strikeIt: boolean) {
    const p = pull.current;
    pull.current = null;
    if (!p?.sideways) return;
    swallow.current = true;
    window.setTimeout(() => (swallow.current = false), 0);
    if (line.current) line.current.style.transition = "";
    paint(0);
    if (strikeIt && p.past) onStrike?.();
  }

  return (
    <li className="group/item relative" data-no-swipe>
      <div className="flex items-stretch">
        <button
          type="button"
          role="checkbox"
          aria-checked={item.done}
          aria-disabled={struck || undefined}
          onClick={() => {
            if (swallow.current || struck) return;
            onToggle();
          }}
          onKeyDown={(e) => {
            if ((e.key === "Delete" || e.key === "Backspace") && onStrike && !struck) {
              e.preventDefault();
              onStrike();
            }
          }}
          onPointerDown={(e) => {
            if (e.pointerType === "mouse" || !onStrike || struck) return;
            pull.current = { x: e.clientX, y: e.clientY, sideways: null, past: false };
          }}
          onPointerMove={(e) => {
            const p = pull.current;
            if (!p) return;
            const dx = e.clientX - p.x;
            const dy = e.clientY - p.y;
            if (p.sideways === null) {
              if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return;
              p.sideways = dx < 0 && Math.abs(dx) > Math.abs(dy) * 1.2;
              if (!p.sideways) {
                pull.current = null;
                return;
              }
              e.currentTarget.setPointerCapture(e.pointerId);
              if (line.current) line.current.style.transition = "none";
            }
            const progress = Math.min(1, Math.max(0, -dx) / STRIKE);
            const past = progress >= 1;
            if (past && !p.past) navigator.vibrate?.(8);
            p.past = past;
            paint(progress);
          }}
          onPointerUp={() => release(true)}
          onPointerCancel={() => release(false)}
          className="relative flex min-h-11 min-w-0 flex-1 touch-pan-y items-start gap-3 rounded-lg px-2 py-2.5 text-left outline-none select-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-9 mouse:py-2"
        >
          <Mark done={item.done} draw={draw} faint={struck} />
          <span className="min-w-0 flex-1">
            <span
              className={cn(
                "relative inline-block max-w-full text-[14px] leading-snug transition-colors duration-200 motion-reduce:transition-none",
                struck ? "text-faint line-through decoration-reject/80" : item.done ? "text-faint" : "text-foreground",
              )}
            >
              {item.text}
              {/* The pull's line: drawn from the right across the words. */}
              <span
                ref={line}
                aria-hidden
                className="pointer-events-none absolute inset-x-0 top-[0.66em] h-[1.5px] origin-right scale-x-0 rounded-full bg-reject transition-transform duration-200 ease-out"
              />
            </span>
          </span>
          <span
            ref={hint}
            aria-hidden
            className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded-md bg-background/80 px-1.5 text-[12.5px] text-reject opacity-0"
          >
            빼기
          </span>
        </button>
        {struck ? (
          <button
            type="button"
            onClick={onUnstrike}
            className="anim-settle flex min-h-11 shrink-0 items-center rounded-lg px-2.5 text-[12.5px] font-medium text-foreground underline underline-offset-4 outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-9"
          >
            되돌리기
          </button>
        ) : onStrike ? (
          // With a pointer there is no pull: the row offers the same, on hover.
          <button
            type="button"
            onClick={onStrike}
            aria-label={`${item.text} 빼기`}
            title="빼기"
            className="hidden size-9 shrink-0 items-center justify-center self-center rounded-md text-faint opacity-0 transition-opacity outline-none group-hover/item:opacity-100 hover:text-reject focus-visible:opacity-100 focus-visible:ring-3 focus-visible:ring-ring/50 pointer-fine:flex"
          >
            <X aria-hidden className="size-3.5" />
          </button>
        ) : null}
      </div>
    </li>
  );
}

/** The ring an item is ticked in; filled, with the mark drawn, once done. */
function Mark({ done, draw, faint }: { done: boolean; draw: boolean; faint?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "mt-px flex size-5 shrink-0 items-center justify-center rounded-full border-[1.5px] transition-colors duration-150 motion-reduce:transition-none",
        faint ? "border-edge" : done ? "border-foreground bg-foreground" : "border-dim group-hover/item:border-foreground/80",
        faint && done && "bg-edge",
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

/** Closing a list, where a list may be closed from: one tap, said plainly. */
function CloseList({ onClose }: { onClose: () => Promise<string | null> }) {
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<string | null>(null);
  return (
    <div className="mt-1 flex items-center justify-end gap-2">
      {said ? <span className="text-[12px] text-reject">{said}</span> : null}
      <button
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setSaid(await onClose());
          setBusy(false);
        }}
        className="tap rounded-lg px-2 text-[12.5px] text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
      >
        목록 닫기
      </button>
    </div>
  );
}

/**
 * A checklist shown in the conversation on its own: lit, not framed - a
 * frame is for a decision, and nothing here waits on one.
 */
export function ChecklistCard({
  list,
  title,
  onTick,
  onDrop,
  onClose,
  fold,
}: {
  list?: Checklist;
  title?: string;
  onTick: Tick;
  onDrop?: Drop;
  onClose?: () => Promise<string | null>;
  fold?: Fold;
}) {
  return (
    <section
      className={cn(
        "w-full max-w-[34rem] rounded-xl bg-glass-raised px-4 pt-3.5 sm:px-5",
        fold && !fold.open ? "pb-3.5" : "pb-2",
      )}
    >
      <ChecklistBody list={list} title={title} onTick={onTick} onDrop={onDrop} onClose={onClose} fold={fold} />
    </section>
  );
}
