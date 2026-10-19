"use client";

import { useCallback, useEffect, useImperativeHandle, useLayoutEffect, useRef, type Ref } from "react";
import { flushSync } from "react-dom";
import { MonthGrid, type DayMark } from "@/components/calendar/month-grid";
import { shiftMonth, type Cursor } from "@/lib/month";

/** How far a page must be pulled, as a share of its width, to turn. */
const TURN_AT = 0.22;
/** A quick flick turns it sooner: at least this fast (px per ms) and this far (px). */
const FLICK_SPEED = 0.45;
const FLICK_MIN = 36;
/** A drag is sideways only if its first this-many px went more sideways than up or down. */
const DECIDE_PX = 10;
const GLIDE_MS = 220;
const SETTLE_MS = 180;
/** Decelerating into place, the way a page laid down comes to rest. */
const EASE = "cubic-bezier(0.22, 0.61, 0.36, 1)";
/** Sideways trackpad scroll that turns a page; the rest of that gesture is let go. */
const WHEEL_TURN = 60;
const WHEEL_QUIET_MS = 220;

export type MonthSwipeHandle = { go: (by: -1 | 1) => void };

type Drag = { id: number; x0: number; y0: number; sideways: boolean; dx: number; lastX: number; lastT: number; vx: number };

const reduced = () =>
  typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/**
 * The month as a page in a strip of three - last month, this one, next.
 *
 * A finger moves the strip one to one, so the month being pulled in is in
 * view, dots and all, before it is let go. Past a fifth of the width, or on
 * a flick, it turns; short of that it settles back. Each page carries its own
 * weekday row and a gutter separates the pages, so mid-swipe every date stays
 * under its weekday and two months never read as one broken grid (a fixed
 * row over moving dates did). The title and the day below change only once a
 * page has turned. A drag that starts mostly up or down is the page's scroll
 * (touch-action: pan-y keeps that native), and a drag never selects the day
 * it began on. On a trackpad a sideways swipe turns one page per gesture,
 * and the browser's back/forward swipe is kept out of the grid.
 */
export function MonthSwipe({
  ref,
  cursor,
  selected,
  today,
  marks,
  onSelect,
  onPage,
}: {
  ref?: Ref<MonthSwipeHandle>;
  cursor: Cursor;
  selected: string;
  today: string;
  marks: Map<string, DayMark>;
  onSelect: (date: string) => void;
  /** Turn by a month; the selection moves with it. */
  onPage: (by: -1 | 1) => void;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const track = useRef<HTMLDivElement>(null);
  const drag = useRef<Drag | null>(null);
  // A sideways drag happened: the click that may follow it is not a tap.
  const moved = useRef(false);
  // A page is turning; nothing else starts until it has.
  const busy = useRef(false);

  // The strip at a page (-1 last month, 0 this, 1 next) plus a finger's offset.
  const place = useCallback((offset: number, at: -1 | 0 | 1 = 0, ms = 0) => {
    const el = track.current;
    if (!el) return;
    el.style.transition = ms ? `transform ${ms}ms ${EASE}` : "none";
    el.style.transform = `translate3d(calc(${(-(at + 1) * 100) / 3}% + ${offset}px), 0, 0)`;
  }, []);

  // A new month in the middle (turned here, by a button, a key, a day tapped
  // in the spill-over): the strip recentres in the same frame, so the page
  // that slid in stays exactly where it is on screen.
  useLayoutEffect(() => {
    place(0);
    busy.current = false;
  }, [cursor.year, cursor.month, place]);

  const go = useCallback(
    (by: -1 | 1) => {
      if (busy.current) return;
      busy.current = true;
      const el = track.current;
      if (!el || reduced()) {
        onPage(by);
        return;
      }
      let fired = false;
      const done = () => {
        if (fired) return;
        fired = true;
        el.removeEventListener("transitionend", done);
        window.clearTimeout(timer);
        // The new month renders and the strip recentres in one go - no frame
        // of the old month in the middle between them.
        flushSync(() => onPage(by));
      };
      const timer = window.setTimeout(done, GLIDE_MS + 120);
      el.addEventListener("transitionend", done);
      place(0, by, GLIDE_MS);
    },
    [onPage, place],
  );

  useImperativeHandle(ref, () => ({ go }), [go]);

  const settle = useCallback(() => place(0, 0, reduced() ? 0 : SETTLE_MS), [place]);

  function onPointerDown(e: React.PointerEvent) {
    moved.current = false;
    // A mouse turns pages with the buttons or a trackpad; dragging is for fingers and pens.
    if (busy.current || e.pointerType === "mouse" || !e.isPrimary) return;
    drag.current = {
      id: e.pointerId,
      x0: e.clientX,
      y0: e.clientY,
      sideways: false,
      dx: 0,
      lastX: e.clientX,
      lastT: e.timeStamp,
      vx: 0,
    };
  }

  function onPointerMove(e: React.PointerEvent) {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    const dx = e.clientX - d.x0;
    const dy = e.clientY - d.y0;
    if (!d.sideways) {
      if (Math.abs(dx) < DECIDE_PX && Math.abs(dy) < DECIDE_PX) return;
      if (Math.abs(dy) >= Math.abs(dx)) {
        // Up or down: the page's own scroll, untouched.
        drag.current = null;
        return;
      }
      d.sideways = true;
      try {
        viewport.current?.setPointerCapture(e.pointerId);
      } catch {
        // A pointer that cannot be captured still moves the page.
      }
    }
    moved.current = true;
    const dt = e.timeStamp - d.lastT;
    if (dt > 0) d.vx = 0.7 * ((e.clientX - d.lastX) / dt) + 0.3 * d.vx;
    d.lastX = e.clientX;
    d.lastT = e.timeStamp;
    d.dx = dx;
    place(dx);
  }

  function onPointerUp(e: React.PointerEvent) {
    const d = drag.current;
    drag.current = null;
    if (!d || d.id !== e.pointerId || !d.sideways) return;
    const width = viewport.current?.clientWidth || 1;
    const far = Math.abs(d.dx) > width * TURN_AT;
    const flick = Math.abs(d.dx) > FLICK_MIN && Math.abs(d.vx) > FLICK_SPEED && Math.sign(d.vx) === Math.sign(d.dx);
    // Pulled left, the next month; right, the last.
    if (far || flick) go(d.dx < 0 ? 1 : -1);
    else settle();
  }

  function onPointerCancel() {
    const d = drag.current;
    drag.current = null;
    if (d?.sideways) settle();
  }

  // Trackpad: a sideways two-finger swipe. Native and not passive, so that
  // it can keep the browser from reading it as back/forward.
  const goRef = useRef(go);
  useEffect(() => {
    goRef.current = go;
  }, [go]);
  useEffect(() => {
    const el = viewport.current;
    if (!el) return;
    let sum = 0;
    let spent = false;
    let quiet = 0;
    const onWheel = (e: WheelEvent) => {
      if (Math.abs(e.deltaX) <= Math.abs(e.deltaY)) return;
      e.preventDefault();
      window.clearTimeout(quiet);
      quiet = window.setTimeout(() => {
        sum = 0;
        spent = false;
      }, WHEEL_QUIET_MS);
      // One page per gesture: its momentum after the turn is let go.
      if (spent) return;
      sum += e.deltaX;
      if (Math.abs(sum) >= WHEEL_TURN) {
        spent = true;
        goRef.current(sum > 0 ? 1 : -1);
      }
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", onWheel);
      window.clearTimeout(quiet);
    };
  }, []);

  const pages: [string, Cursor, boolean][] = [
    ["last", shiftMonth(cursor, -1), true],
    ["this", cursor, false],
    ["next", shiftMonth(cursor, 1), true],
  ];

  return (
    <div>
      {/* The clip reaches 16px past the column on each side and every page is
          inset by the same: at rest a page lines up with the column, while
          turning there is a 32px gutter between pages - and on a phone they
          slide in from the screen's edge. 4px above and below keep a focused
          day's ring clear of the clip. */}
      <div
        ref={viewport}
        className="-mx-4 -my-1 touch-pan-y overflow-hidden overscroll-x-contain"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerCancel}
        onClickCapture={(e) => {
          if (!moved.current) return;
          moved.current = false;
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        <div
          ref={track}
          className="flex w-[300%] will-change-transform"
          style={{ transform: "translate3d(-33.3333%, 0, 0)" }}
        >
          {pages.map(([key, month, passive]) => (
            <div key={key} className="w-1/3 shrink-0 px-4 py-1">
              <MonthGrid
                cursor={month}
                selected={selected}
                today={today}
                marks={marks}
                onSelect={onSelect}
                onPage={passive ? undefined : go}
                passive={passive}
              />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
