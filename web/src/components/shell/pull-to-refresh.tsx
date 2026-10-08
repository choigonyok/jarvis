"use client";

import { useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { cn } from "@/lib/utils";

/** How far the finger travels (after damping) before letting go reloads. */
const TRIGGER = 72;
const MAX = 110;

/**
 * Pull down from the top to reload, the way a phone browser does - which a
 * home-screen app does not, and every tab here scrolls inside its own panel
 * anyway, so the browser's own gesture would never fire.
 *
 * It only starts when whatever is under the finger is already scrolled to the
 * top, the pull is mostly vertical, and the touch is not in a sheet, the
 * remote screen, or a text field.
 */
export function PullToRefresh() {
  const [pull, setPull] = useState(0);
  const [reloading, setReloading] = useState(false);
  const g = useRef<{ x: number; y: number; active: boolean; decided: boolean } | null>(null);

  useEffect(() => {
    const scrollerOf = (node: Element | null): Element | null => {
      for (let el = node; el && el !== document.body; el = el.parentElement) {
        const oy = getComputedStyle(el).overflowY;
        if ((oy === "auto" || oy === "scroll") && el.scrollHeight > el.clientHeight) return el;
      }
      return null;
    };
    const excluded = (t: Element) =>
      Boolean(t.closest('[role="dialog"], [data-no-pull], input, textarea, select, canvas, [contenteditable="true"]'));

    const start = (e: TouchEvent) => {
      if (e.touches.length !== 1 || reloadingRef.current) return;
      const t = e.target as Element;
      if (excluded(t)) return;
      const scroller = scrollerOf(t);
      if (scroller && scroller.scrollTop > 0) return;
      g.current = { x: e.touches[0].clientX, y: e.touches[0].clientY, active: false, decided: false };
    };
    const move = (e: TouchEvent) => {
      const s = g.current;
      if (!s) return;
      const dx = e.touches[0].clientX - s.x;
      const dy = e.touches[0].clientY - s.y;
      if (!s.decided) {
        if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return;
        s.decided = true;
        // Downward and mostly vertical, or not ours at all.
        s.active = dy > 0 && Math.abs(dy) > Math.abs(dx) * 1.5;
        if (!s.active) {
          g.current = null;
          return;
        }
      }
      if (!s.active) return;
      e.preventDefault();
      setPull(Math.min(MAX, Math.max(0, dy * 0.5)));
    };
    const end = () => {
      const s = g.current;
      g.current = null;
      if (!s?.active) return;
      setPull((p) => {
        if (p >= TRIGGER) {
          reloadingRef.current = true;
          setReloading(true);
          window.setTimeout(() => window.location.reload(), 150);
          return TRIGGER;
        }
        return 0;
      });
    };

    document.addEventListener("touchstart", start, { passive: true });
    document.addEventListener("touchmove", move, { passive: false });
    document.addEventListener("touchend", end);
    document.addEventListener("touchcancel", end);
    return () => {
      document.removeEventListener("touchstart", start);
      document.removeEventListener("touchmove", move);
      document.removeEventListener("touchend", end);
      document.removeEventListener("touchcancel", end);
    };
  }, []);

  const reloadingRef = useRef(false);
  if (pull === 0 && !reloading) return null;
  const ready = pull >= TRIGGER;

  return (
    <div
      aria-hidden
      className="pointer-events-none fixed inset-x-0 top-0 z-[60] flex justify-center"
      style={{ transform: `translateY(calc(env(safe-area-inset-top) + ${pull - 28}px))` }}
    >
      <span
        className={cn(
          "flex size-9 items-center justify-center rounded-full border border-edge bg-background/90 shadow-lg backdrop-blur-md transition-colors",
          ready ? "text-foreground" : "text-faint",
        )}
      >
        <RefreshCw
          className={cn("size-4", reloading && "animate-spin motion-reduce:animate-none")}
          style={reloading ? undefined : { transform: `rotate(${(pull / TRIGGER) * 270}deg)` }}
        />
      </span>
    </div>
  );
}
