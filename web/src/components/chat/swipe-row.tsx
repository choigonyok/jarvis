"use client";

import { useRef, useState, type ReactNode } from "react";
import { Reply } from "lucide-react";
import { cn } from "@/lib/utils";

/** How far a message has to travel before letting go quotes it. */
const THRESHOLD = 56;
const MAX = 84;

/**
 * A message that can be pulled left to quote it, the way KakaoTalk replies.
 *
 * Only a sideways pull on a touch screen is taken: a vertical move is the
 * thread scrolling (touch-action: pan-y keeps that native), and a mouse never
 * starts it - on a pointer the quote is the small button beside the time.
 * Past the threshold the phone ticks once, so the release is a decision
 * rather than a guess.
 */
export function SwipeRow({
  children,
  onRef,
  className,
}: {
  children: ReactNode;
  onRef: () => void;
  /** Layout for the message itself (the part that moves). */
  className?: string;
}) {
  const [dx, setDx] = useState(0);
  const [dragging, setDragging] = useState(false);
  const start = useRef<{ x: number; y: number; sideways: boolean | null } | null>(null);
  const armed = useRef(false);

  function reset() {
    start.current = null;
    armed.current = false;
    setDragging(false);
    setDx(0);
  }

  return (
    <div
      className="relative touch-pan-y"
      onPointerDown={(e) => {
        if (e.pointerType === "mouse") return;
        // A field on a card moves its caret sideways; that pull is not a quote.
        if ((e.target as Element).closest("input, textarea, select, [contenteditable='true'], [data-no-swipe]")) return;
        start.current = { x: e.clientX, y: e.clientY, sideways: null };
        armed.current = false;
      }}
      onPointerMove={(e) => {
        const s = start.current;
        if (!s) return;
        const ddx = e.clientX - s.x;
        const ddy = e.clientY - s.y;
        if (s.sideways === null) {
          if (Math.abs(ddx) < 8 && Math.abs(ddy) < 8) return;
          s.sideways = ddx < 0 && Math.abs(ddx) > Math.abs(ddy) * 1.2;
          if (!s.sideways) {
            start.current = null;
            return;
          }
          e.currentTarget.setPointerCapture(e.pointerId);
          setDragging(true);
        }
        // Resistance past the threshold, so the message never flies off.
        const raw = Math.min(0, ddx);
        const v = raw < -THRESHOLD ? -THRESHOLD + (raw + THRESHOLD) * 0.35 : raw;
        setDx(Math.max(-MAX, v));
        const past = v <= -THRESHOLD;
        if (past && !armed.current) navigator.vibrate?.(8);
        armed.current = past;
      }}
      onPointerUp={() => {
        if (armed.current) onRef();
        reset();
      }}
      onPointerCancel={reset}
    >
      <div
        className={cn(className, !dragging && "transition-transform duration-200 ease-out motion-reduce:transition-none")}
        style={dx ? { transform: `translateX(${dx}px)` } : undefined}
      >
        {children}
      </div>
      <span
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-1 flex size-8 -translate-y-1/2 items-center justify-center rounded-full text-dim"
        style={{
          opacity: Math.min(1, -dx / THRESHOLD),
          transform: `translateY(-50%) scale(${-dx >= THRESHOLD ? 1.12 : 0.85 + (-dx / THRESHOLD) * 0.15})`,
          background: -dx >= THRESHOLD ? "oklch(1 0 0 / 0.1)" : undefined,
        }}
      >
        <Reply className="size-4" />
      </span>
    </div>
  );
}
