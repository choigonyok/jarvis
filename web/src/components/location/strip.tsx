"use client";

import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { type Day, type Mode, type Scene, hm } from "@/lib/track";
import { cn } from "@/lib/utils";

/** A way's mark on the strip, in its person's color and as bright as its light on the map. */
const MARK: Record<"mine" | "partner", Record<Mode, string>> = {
  mine: { drive: "bg-mine/85", move: "bg-mine/70", walk: "bg-mine/55", gap: "bg-mine/25" },
  partner: { drive: "bg-partner/85", move: "bg-partner/70", walk: "bg-partner/55", gap: "bg-partner/25" },
};

/** A stay's block: plain for one person, faintly in their color beside the other's. */
const STAY = { one: "bg-glass-raised", mine: "bg-mine/15", partner: "bg-partner/15" } as const;

/**
 * The day as one strip, midnight to midnight: the stays as blocks with their
 * names, the ways between them as bright marks. It is the day's shape at a
 * glance and the replay's scrubber at once - the part not yet replayed is
 * dimmed, and a press or a drag anywhere on it goes to that moment (the
 * calendar's legend and the ledger's rows are their filters the same way:
 * what explains a thing is where you work it). Both people: a row each, the
 * viewer's on top, and one hand for the two.
 */
export function Strip({
  scene: day,
  t,
  onSeek,
  onHold,
}: {
  scene: Scene;
  t: number;
  onSeek: (t: number) => void;
  /** A finger is on it: the replay waits. */
  onHold: (held: boolean) => void;
}) {
  const bar = useRef<HTMLDivElement>(null);
  const [px, setPx] = useState(0);
  const held = useRef(false);
  const whole = day.dayEnd - day.start;
  const at = (x: number) => ((Math.min(Math.max(x, day.start), day.dayEnd) - day.start) / whole) * 100;

  // Its width in pixels decides which blocks have room for a name.
  useEffect(() => {
    const el = bar.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setPx(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  function seekAt(clientX: number) {
    const box = bar.current?.getBoundingClientRect();
    if (!box || box.width === 0) return;
    const f = Math.min(1, Math.max(0, (clientX - box.left) / box.width));
    onSeek(Math.min(day.end, day.start + f * whole));
  }

  function down(e: PointerEvent<HTMLDivElement>) {
    if (e.button !== 0) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    held.current = true;
    onHold(true);
    seekAt(e.clientX);
  }
  function move(e: PointerEvent<HTMLDivElement>) {
    if (held.current) seekAt(e.clientX);
  }
  function up() {
    if (!held.current) return;
    held.current = false;
    onHold(false);
  }

  function key(e: KeyboardEvent<HTMLDivElement>) {
    const minute = 60_000;
    const by: Record<string, number> = {
      ArrowLeft: -5 * minute,
      ArrowRight: 5 * minute,
      PageDown: -60 * minute,
      PageUp: 60 * minute,
    };
    let next: number | null = null;
    if (e.key in by) next = t + by[e.key] * (e.shiftKey && e.key.startsWith("Arrow") ? 12 : 1);
    else if (e.key === "Home") next = day.start;
    else if (e.key === "End") next = day.end;
    if (next === null) return;
    e.preventDefault();
    onSeek(Math.min(day.end, Math.max(day.start, next)));
  }

  const nowAt = at(day.end);
  return (
    <div>
      <div
        ref={bar}
        role="slider"
        tabIndex={0}
        aria-label="하루 중 시각"
        aria-valuemin={0}
        aria-valuemax={Math.round((day.end - day.start) / 60_000)}
        aria-valuenow={Math.round((t - day.start) / 60_000)}
        aria-valuetext={hm(t, day.dayEnd)}
        data-no-pull
        onPointerDown={down}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={up}
        onKeyDown={key}
        className="relative h-11 cursor-pointer touch-none rounded-lg outline-none select-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <div className="absolute inset-x-0 top-2 bottom-2 overflow-hidden rounded-[7px] bg-glass">
          {day.people.map((person, row) => (
            <Row
              key={person.who}
              day={person.day}
              tone={person.me ? "mine" : "partner"}
              single={day.people.length === 1}
              row={row}
              at={at}
              px={px}
            />
          ))}
          {/* What the replay has not reached yet. */}
          <span className="absolute inset-y-0 right-0 bg-background/55" style={{ left: `${at(t)}%` }} />
          {/* Today, the hours still to come. */}
          {day.today ? <span className="absolute inset-y-0 right-0 bg-background/50" style={{ left: `${nowAt}%` }} /> : null}
        </div>
        <span
          aria-hidden
          className="absolute top-0.5 bottom-0.5 w-[2px] -translate-x-1/2 rounded-full bg-foreground"
          style={{ left: `${at(t)}%` }}
        />
      </div>
      <Hours day={day} />
    </div>
  );
}

/** One person's row: the stays as blocks (named where there is room), the ways as marks. */
function Row({
  day,
  tone,
  single,
  row,
  at,
  px,
}: {
  day: Day;
  tone: "mine" | "partner";
  single: boolean;
  row: number;
  at: (x: number) => number;
  px: number;
}) {
  // Two rows split the height, with a hairline between.
  const band = single ? { top: "0%", height: "100%" } : { top: row === 0 ? "0%" : "54%", height: "46%" };
  return (
    <div className="absolute inset-x-0" style={band}>
      {day.items.map((it) => {
        const left = at(it.start);
        const width = at(it.end) - left;
        if (it.type === "move") {
          return (
            <span
              key={`m${it.start}`}
              className={cn("absolute inset-y-0 min-w-[2px] rounded-[1.5px]", MARK[tone][it.mode])}
              style={{ left: `${left}%`, width: `${width}%` }}
            />
          );
        }
        const room = (width / 100) * px;
        return (
          <span
            key={`s${it.start}`}
            className={cn(
              "absolute inset-y-0 flex items-center overflow-hidden border-x border-background",
              single ? STAY.one : STAY[tone],
            )}
            style={{ left: `${left}%`, width: `${width}%` }}
          >
            {single && it.node.name && room >= 30 ? (
              <span className="truncate px-1.5 text-[10.5px] text-dim">{it.node.name}</span>
            ) : null}
          </span>
        );
      })}
    </div>
  );
}

/** Under the strip: the hours of the day, and today, where now is. */
function Hours({ day }: { day: Scene }) {
  const nowAt = ((day.end - day.start) / (day.dayEnd - day.start)) * 100;
  return (
    <div aria-hidden className="tnum relative mt-0.5 h-4 text-[10.5px] text-faint">
      {[0, 6, 12, 18, 24].map((h) => {
        const left = (h / 24) * 100;
        // Today, the hour under 지금 gives it the room.
        if (day.today && Math.abs(left - nowAt) < 7) return null;
        return (
          <span
            key={h}
            className={cn("absolute", h === 0 ? "" : h === 24 ? "-translate-x-full" : "-translate-x-1/2")}
            style={{ left: `${left}%` }}
          >
            {h}
          </span>
        );
      })}
      {day.today ? (
        <span
          className={cn("absolute text-dim", nowAt > 94 ? "-translate-x-full" : nowAt < 6 ? "" : "-translate-x-1/2")}
          style={{ left: `${nowAt}%` }}
        >
          지금
        </span>
      ) : null}
    </div>
  );
}
