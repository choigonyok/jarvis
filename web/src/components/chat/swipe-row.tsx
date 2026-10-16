"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { Reply } from "lucide-react";
import { cn } from "@/lib/utils";

/** How far a message has to travel before letting go quotes it. */
const THRESHOLD = 56;
const MAX = 84;
/** The snap back after letting go (duration-200), and a frame to spare. */
const SETTLE_MS = 240;

type Pull = { x: number; y: number; sideways: boolean | null; dx: number; armed: boolean };

/**
 * A message that can be pulled left to quote it, the way KakaoTalk replies.
 *
 * Only a sideways pull on a touch screen is taken: a vertical move is the
 * thread scrolling (touch-action: pan-y keeps that native), and a mouse never
 * starts it - on a pointer the quote is the small button beside the time.
 * Past the threshold the phone ticks once, so the release is a decision
 * rather than a guess.
 *
 * The pull is written straight onto the message, at most once a frame, with
 * the message on a layer of its own until it has snapped back. Rendering it
 * through React on every pointer move, and repainting the message each time,
 * is what made it stutter on a phone.
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
  const body = useRef<HTMLDivElement>(null);
  const icon = useRef<HTMLSpanElement>(null);
  const pull = useRef<Pull | null>(null);
  const frame = useRef(0);
  const settle = useRef(0);

  useEffect(
    () => () => {
      cancelAnimationFrame(frame.current);
      window.clearTimeout(settle.current);
    },
    [],
  );

  function paint() {
    frame.current = 0;
    const p = pull.current;
    const b = body.current;
    const i = icon.current;
    if (!p || !b || !i) return;
    b.style.transform = `translate3d(${p.dx}px, 0, 0)`;
    const shown = Math.min(1, -p.dx / THRESHOLD);
    i.style.opacity = String(shown);
    i.style.scale = String(p.armed ? 1.12 : 0.85 + shown * 0.15);
    i.style.background = p.armed ? "oklch(1 0 0 / 0.1)" : "";
  }

  function end(quote: boolean) {
    const p = pull.current;
    pull.current = null;
    if (!p?.sideways) return;
    cancelAnimationFrame(frame.current);
    frame.current = 0;
    const b = body.current;
    const i = icon.current;
    if (b) {
      // Back into place as a transition on its layer; the layer goes once it is there.
      delete b.dataset.dragging;
      b.style.transform = "";
      window.clearTimeout(settle.current);
      settle.current = window.setTimeout(() => {
        if (!pull.current) b.style.willChange = "";
      }, SETTLE_MS);
    }
    if (i) {
      i.style.opacity = "";
      i.style.scale = "";
      i.style.background = "";
    }
    if (quote && p.armed) onRef();
  }

  return (
    <div
      className="relative touch-pan-y"
      onPointerDown={(e) => {
        if (e.pointerType === "mouse") return;
        // A field on a card moves its caret sideways; that pull is not a quote.
        if ((e.target as Element).closest("input, textarea, select, [contenteditable='true'], [data-no-swipe]")) return;
        pull.current = { x: e.clientX, y: e.clientY, sideways: null, dx: 0, armed: false };
      }}
      onPointerMove={(e) => {
        const p = pull.current;
        if (!p) return;
        const ddx = e.clientX - p.x;
        const ddy = e.clientY - p.y;
        if (p.sideways === null) {
          if (Math.abs(ddx) < 8 && Math.abs(ddy) < 8) return;
          p.sideways = ddx < 0 && Math.abs(ddx) > Math.abs(ddy) * 1.2;
          if (!p.sideways) {
            pull.current = null;
            return;
          }
          e.currentTarget.setPointerCapture(e.pointerId);
          const b = body.current;
          if (b) {
            window.clearTimeout(settle.current);
            b.dataset.dragging = "";
            b.style.willChange = "transform";
          }
        }
        // Resistance past the threshold, so the message never flies off.
        const raw = Math.min(0, ddx);
        const v = raw < -THRESHOLD ? -THRESHOLD + (raw + THRESHOLD) * 0.35 : raw;
        p.dx = Math.max(-MAX, v);
        const past = v <= -THRESHOLD;
        if (past && !p.armed) navigator.vibrate?.(8);
        p.armed = past;
        if (!frame.current) frame.current = requestAnimationFrame(paint);
      }}
      onPointerUp={() => end(true)}
      onPointerCancel={() => end(false)}
    >
      <div
        ref={body}
        className={cn(
          className,
          "transition-transform duration-200 ease-out data-dragging:transition-none motion-reduce:transition-none",
        )}
      >
        {children}
      </div>
      <span
        ref={icon}
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-1 flex size-8 -translate-y-1/2 scale-[0.85] items-center justify-center rounded-full text-dim opacity-0"
      >
        <Reply className="size-4" />
      </span>
    </div>
  );
}
