"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Download, X } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

export type LightboxItem = {
  kind?: "image" | "video";
  src: string;
  /** A video's still, shown until it plays. */
  poster?: string;
  alt?: string;
  /** The line over it: when it was taken, what it is. */
  title?: string;
  /** The original, to save; none where there is nothing bigger. */
  download?: string;
};

/** How long a photo takes to slide the rest of the way, once let go. */
const TURN_MS = 260;

/**
 * One photo or video, the whole screen: black ground, a way out, the way to
 * the next one. Everything the console shows full size opens here - the
 * photos tab, a photo in the conversation, the photos on a card, a screen a
 * job took - so a tap on a picture never leaves the app for the browser (a
 * home-screen app would open them in Safari).
 *
 * The photos lie side by side and a finger slides them: the one before or
 * after comes in with it, and once let go a fifth of the way across, or
 * flicked, it slides the rest of the way - a photo that changed in one step
 * as the finger lifted would not read as something to swipe. A finger pulls
 * down to close; two fingers zoom into a photo and one moves it around while
 * it is zoomed, and a double tap zooms in and out - the page itself does not
 * zoom (components/shell/no-zoom.tsx), so the photo has to. Arrow keys move,
 * Esc closes (the dialog's own).
 */
export function Lightbox({ items, start = 0, onClose }: { items: LightboxItem[]; start?: number; onClose: () => void }) {
  const [index, setIndex] = useState(Math.min(Math.max(0, start), items.length - 1));
  const track = useRef<HTMLDivElement>(null);
  const turning = useRef(false);
  const item = items[index];
  const many = items.length > 1;

  /** The row of photos, dx from where the current one fills the screen. */
  const place = useCallback((dx: number, animate: boolean) => {
    const t = track.current;
    if (!t) return;
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    t.style.transition = animate && !still ? `transform ${TURN_MS}ms cubic-bezier(0.2, 0.8, 0.2, 1)` : "none";
    t.style.transform = `translateX(calc(-100% + ${dx}px))`;
  }, []);

  /** Slides on to the next (1) or the one before (-1), when there is one. */
  const turn = useCallback(
    (dir: 1 | -1) => {
      if (turning.current) return;
      const next = index + dir;
      if (next < 0 || next >= items.length) {
        place(0, true);
        return;
      }
      turning.current = true;
      place(-dir * (track.current?.clientWidth ?? 0), true);
      window.setTimeout(() => setIndex(next), matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : TURN_MS);
    },
    [index, items.length, place],
  );

  // The new photo is in the middle now: the row goes back under it, unseen.
  useLayoutEffect(() => {
    place(0, false);
    turning.current = false;
  }, [index, place]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") turn(-1);
      if (e.key === "ArrowRight") turn(1);
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [turn]);

  if (!item) return null;
  const edge = (dx: number) => (dx > 0 && index === 0) || (dx < 0 && index === items.length - 1);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        variant="fullscreen"
        aria-describedby={undefined}
        // The viewer is drawn over the page, but its events still reach the
        // components it was opened from: a portal bubbles where React put
        // it. Opened from a message, a finger sliding the photos was the
        // message's own pull to quote (chat/swipe-row.tsx), which took the
        // finger on its first move - the photos never slid on a phone.
        // Nothing that happens in here goes out.
        onPointerDown={stopHere}
        onPointerMove={stopHere}
        onPointerUp={stopHere}
        onPointerCancel={stopHere}
        onClick={stopHere}
      >
        <DialogTitle className="sr-only">{item.alt || "사진 보기"}</DialogTitle>
        <div className="pt-safe relative z-10 flex shrink-0 items-center gap-2 px-2 py-2 text-white">
          <button
            type="button"
            onClick={onClose}
            aria-label="닫기"
            className="flex size-11 items-center justify-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-white/50"
          >
            <X aria-hidden className="size-5" />
          </button>
          <p className="tnum min-w-0 flex-1 truncate text-aside text-white/75">
            {item.title ?? ""}
            {many ? (
              <span className={cn("text-white/45", item.title && "ms-2")}>
                {index + 1} / {items.length}
              </span>
            ) : null}
          </p>
          {item.download ? (
            <a
              href={item.download}
              download
              aria-label="원본 받기"
              className="flex size-11 items-center justify-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-white/50"
            >
              <Download aria-hidden className="size-5" />
            </a>
          ) : null}
        </div>
        <div className="pb-safe relative min-h-0 flex-1 overflow-hidden">
          <div ref={track} className="flex h-full w-full will-change-transform" style={{ transform: "translateX(-100%)" }}>
            {[index - 1, index, index + 1].map((i, slot) => {
              const it = items[i];
              // Keyed by the photo, so the one sliding in stays the same
              // element when it becomes the current one. A little black
              // between two of them as they slide, as in the phone's own.
              return (
                <div key={it ? `${i}:${it.src}` : `none-${slot}`} className="relative flex h-full w-full shrink-0 items-center justify-center px-2.5">
                  {!it ? null : it.kind === "video" ? (
                    slot === 1 ? (
                      <video src={it.src} poster={it.poster} controls autoPlay playsInline className="max-h-full max-w-full" />
                    ) : it.poster ? (
                      // eslint-disable-next-line @next/next/no-img-element -- served through the console's own proxy
                      <img src={it.poster} alt="" draggable={false} className="max-h-full max-w-full object-contain" />
                    ) : null
                  ) : (
                    <Zoomable
                      src={it.src}
                      alt={it.alt ?? ""}
                      active={slot === 1}
                      onPan={(dx) => place(edge(dx) ? dx * 0.3 : dx, false)}
                      onPanEnd={(dx, v) => {
                        const w = track.current?.clientWidth ?? 1;
                        if (!edge(dx) && (Math.abs(dx) > w * 0.2 || Math.abs(v) > 0.45)) turn(dx < 0 ? 1 : -1);
                        else place(0, true);
                      }}
                      onClose={onClose}
                    />
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const stopHere = (e: { stopPropagation: () => void }) => e.stopPropagation();

const MAX_SCALE = 4;
const TAP_SCALE = 2.5;
/** How far a finger moves before it is a slide or a pull, not a tap. */
const SLOP = 8;
/** How far down a pull goes before letting go closes the viewer. */
const FLICK_Y = 120;
const DOUBLE_TAP_MS = 300;

/**
 * A photo that zooms under two fingers and a double tap, and moves under one
 * while zoomed. At its own size a one-finger slide sideways is handed up
 * (onPan, onPanEnd: the row of photos follows it), and a pull down closes.
 * Everything is written straight onto the elements, not through React, so it
 * follows the fingers frame for frame on a phone. Only the current photo
 * (active) takes a finger.
 */
function Zoomable({
  src,
  alt,
  active,
  onPan,
  onPanEnd,
  onClose,
}: {
  src: string;
  alt: string;
  active: boolean;
  onPan: (dx: number) => void;
  /** Let go after a slide: how far, and how fast (px/ms) at the end. */
  onPanEnd: (dx: number, v: number) => void;
  onClose: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const el = useRef<HTMLDivElement>(null);
  const st = useRef({ s: 1, tx: 0, ty: 0 });
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const pinch = useRef<{ d0: number; s0: number; mx: number; my: number; tx0: number; ty0: number } | null>(null);
  const drag = useRef<{
    x0: number;
    y0: number;
    tx0: number;
    ty0: number;
    dx: number;
    dy: number;
    /** Which way a finger at the photo's own size is going, once it has gone SLOP. */
    axis: "x" | "y" | null;
    moved: boolean;
    t: number;
    v: number;
  } | null>(null);
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);

  // Gone from the middle (slid away), it comes back at its own size.
  useEffect(() => {
    if (active) return;
    st.current = { s: 1, tx: 0, ty: 0 };
    if (el.current) {
      el.current.style.transition = "none";
      el.current.style.transform = "";
    }
  }, [active]);

  function paint(animate = false) {
    const e = el.current;
    if (!e) return;
    const { s, tx, ty } = st.current;
    e.style.transition = animate ? "transform 180ms ease-out" : "none";
    e.style.transform = `translate(${tx}px, ${ty}px) scale(${s})`;
  }

  /** Keeps the photo over the screen: never dragged off its edge, centred when it is smaller. */
  function clamp() {
    const b = box.current;
    const c = st.current;
    if (!b) return;
    if (c.s <= 1) {
      c.s = 1;
      c.tx = 0;
      c.ty = 0;
      return;
    }
    const w = b.clientWidth;
    const h = b.clientHeight;
    c.tx = Math.min(0, Math.max(w - w * c.s, c.tx));
    c.ty = Math.min(0, Math.max(h - h * c.s, c.ty));
  }

  /** Scales to `s` with the point (x, y) of the screen staying over the same bit of photo. */
  function zoomAt(x: number, y: number, s: number) {
    const c = st.current;
    const next = Math.min(MAX_SCALE, Math.max(1, s));
    c.tx = x - ((x - c.tx) / c.s) * next;
    c.ty = y - ((y - c.ty) / c.s) * next;
    c.s = next;
    clamp();
  }

  const local = (e: React.PointerEvent) => {
    const r = box.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  return (
    <div
      ref={box}
      className={cn("relative h-full w-full touch-none overflow-hidden select-none", !active && "pointer-events-none")}
      onPointerDown={(e) => {
        const p = local(e);
        pointers.current.set(e.pointerId, p);
        e.currentTarget.setPointerCapture(e.pointerId);
        if (pointers.current.size === 2) {
          const [a, b] = [...pointers.current.values()];
          pinch.current = {
            d0: Math.hypot(b.x - a.x, b.y - a.y) || 1,
            s0: st.current.s,
            mx: (a.x + b.x) / 2,
            my: (a.y + b.y) / 2,
            tx0: st.current.tx,
            ty0: st.current.ty,
          };
          // A slide begun by the first finger gives way to the pinch.
          if (drag.current?.axis === "x") onPanEnd(0, 0);
          drag.current = null;
        } else if (pointers.current.size === 1) {
          // On the screen, not in the photo's box: the box slides with the
          // finger, and measured in it a slide came to half its length.
          drag.current = { x0: e.clientX, y0: e.clientY, tx0: st.current.tx, ty0: st.current.ty, dx: 0, dy: 0, axis: null, moved: false, t: e.timeStamp, v: 0 };
        }
      }}
      onPointerMove={(e) => {
        if (!pointers.current.has(e.pointerId)) return;
        const p = local(e);
        pointers.current.set(e.pointerId, p);
        const pz = pinch.current;
        if (pz && pointers.current.size >= 2) {
          const [a, b] = [...pointers.current.values()];
          const d = Math.hypot(b.x - a.x, b.y - a.y) || 1;
          const mx = (a.x + b.x) / 2;
          const my = (a.y + b.y) / 2;
          const s = Math.min(MAX_SCALE, Math.max(0.7, pz.s0 * (d / pz.d0)));
          // The bit of photo that was under the fingers stays under them.
          st.current.s = s;
          st.current.tx = mx - ((pz.mx - pz.tx0) / pz.s0) * s;
          st.current.ty = my - ((pz.my - pz.ty0) / pz.s0) * s;
          if (s >= 1) clamp();
          paint();
          return;
        }
        const d = drag.current;
        if (!d || pointers.current.size !== 1) return;
        const dx = e.clientX - d.x0;
        const dy = e.clientY - d.y0;
        // How fast it is going as it goes: what a flick is told by.
        const dt = e.timeStamp - d.t;
        if (dt > 0) d.v = (dx - d.dx) / dt;
        d.t = e.timeStamp;
        d.dx = dx;
        d.dy = dy;
        if (Math.abs(dx) > SLOP || Math.abs(dy) > SLOP) d.moved = true;
        if (st.current.s > 1) {
          st.current.tx = d.tx0 + dx;
          st.current.ty = d.ty0 + dy;
          clamp();
          paint();
          return;
        }
        if (!d.axis && d.moved) d.axis = Math.abs(dx) > Math.abs(dy) ? "x" : "y";
        if (d.axis === "x") onPan(dx);
        else if (d.axis === "y" && el.current) {
          // A pull down: the photo comes down with the finger, a little behind it.
          el.current.style.transition = "none";
          el.current.style.transform = `translate(0, ${Math.max(0, dy) * 0.6}px)`;
        }
      }}
      onPointerUp={(e) => {
        pointers.current.delete(e.pointerId);
        const p = local(e);
        if (pinch.current && pointers.current.size < 2) {
          pinch.current = null;
          if (st.current.s < 1.05) {
            st.current = { s: 1, tx: 0, ty: 0 };
          }
          clamp();
          paint(true);
          drag.current = null;
          lastTap.current = null;
          return;
        }
        const d = drag.current;
        drag.current = null;
        if (!d) return;
        if (st.current.s <= 1) {
          if (d.axis === "x") {
            // Held still before letting go is not a flick.
            onPanEnd(d.dx, e.timeStamp - d.t > 80 ? 0 : d.v);
            return;
          }
          if (d.axis === "y") {
            if (d.dy > FLICK_Y) onClose();
            else paint(true);
            return;
          }
        } else if (d.moved) {
          return;
        }
        // A tap. Two in a row, close together: zoom in, or back out.
        const now = e.timeStamp;
        const last = lastTap.current;
        if (last && now - last.t < DOUBLE_TAP_MS && Math.hypot(p.x - last.x, p.y - last.y) < 30) {
          lastTap.current = null;
          if (st.current.s > 1) st.current = { s: 1, tx: 0, ty: 0 };
          else zoomAt(p.x, p.y, TAP_SCALE);
          paint(true);
          return;
        }
        lastTap.current = { t: now, x: p.x, y: p.y };
      }}
      onPointerCancel={(e) => {
        pointers.current.delete(e.pointerId);
        if (drag.current?.axis === "x") onPanEnd(0, 0);
        pinch.current = null;
        drag.current = null;
        clamp();
        paint(true);
      }}
    >
      <div ref={el} className="flex h-full w-full origin-top-left items-center justify-center will-change-transform">
        {/* eslint-disable-next-line @next/next/no-img-element -- served through the console's own proxy */}
        <img src={src} alt={alt} draggable={false} className="max-h-full max-w-full object-contain select-none" />
      </div>
    </div>
  );
}
