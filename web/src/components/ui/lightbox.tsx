"use client";

import { useCallback, useEffect, useRef, useState } from "react";
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

/**
 * One photo or video, the whole screen: black ground, a way out, the way to
 * the next one. Everything the console shows full size opens here - the
 * photos tab, a photo in the conversation, a screen a job took - so a tap on
 * a picture never leaves the app for the browser (a home-screen app opened
 * them in Safari, 2026-11-06 review).
 *
 * A finger moves between them, or pulls down to close; two fingers zoom into
 * a photo and one moves it around while it is zoomed, and a double tap zooms
 * in and out - the page itself does not zoom (components/shell/no-zoom.tsx),
 * so the photo has to. Arrow keys move, Esc closes (the dialog's own).
 */
export function Lightbox({ items, start = 0, onClose }: { items: LightboxItem[]; start?: number; onClose: () => void }) {
  const [index, setIndex] = useState(Math.min(Math.max(0, start), items.length - 1));
  const go = useCallback((by: number) => setIndex((i) => Math.min(items.length - 1, Math.max(0, i + by))), [items.length]);
  const item = items[index];

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") go(-1);
      if (e.key === "ArrowRight") go(1);
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [go]);

  if (!item) return null;
  const many = items.length > 1;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent variant="fullscreen" aria-describedby={undefined}>
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
        <div className="pb-safe relative flex min-h-0 flex-1 items-center justify-center overflow-hidden">
          {item.kind === "video" ? (
            <video key={item.src} src={item.src} poster={item.poster} controls autoPlay playsInline className="max-h-full max-w-full" />
          ) : (
            <Zoomable
              key={item.src}
              src={item.src}
              alt={item.alt ?? ""}
              onFlick={(dir) => {
                if (dir === "close") onClose();
                else if (many) go(dir === "next" ? 1 : -1);
              }}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

const MAX_SCALE = 4;
const TAP_SCALE = 2.5;
/** How far a finger goes before letting go turns the page, or pulls the viewer down. */
const FLICK_X = 60;
const FLICK_Y = 120;
const DOUBLE_TAP_MS = 300;

type Flick = "next" | "prev" | "close";

/**
 * A photo that zooms under two fingers and a double tap, and moves under one
 * while zoomed. At its own size a one-finger pull is handed up (onFlick): the
 * next or the last photo, or down to close. Everything is written straight
 * onto the element, not through React, so it follows the fingers frame for
 * frame on a phone.
 */
function Zoomable({ src, alt, onFlick }: { src: string; alt: string; onFlick: (dir: Flick) => void }) {
  const box = useRef<HTMLDivElement>(null);
  const el = useRef<HTMLDivElement>(null);
  const st = useRef({ s: 1, tx: 0, ty: 0 });
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const pinch = useRef<{ d0: number; s0: number; mx: number; my: number; tx0: number; ty0: number } | null>(null);
  const drag = useRef<{ x0: number; y0: number; tx0: number; ty0: number; dx: number; dy: number; moved: boolean } | null>(null);
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);

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
      className="relative h-full w-full touch-none overflow-hidden select-none"
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
          drag.current = null;
        } else if (pointers.current.size === 1) {
          drag.current = { x0: p.x, y0: p.y, tx0: st.current.tx, ty0: st.current.ty, dx: 0, dy: 0, moved: false };
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
        d.dx = p.x - d.x0;
        d.dy = p.y - d.y0;
        if (Math.abs(d.dx) > 6 || Math.abs(d.dy) > 6) d.moved = true;
        if (st.current.s > 1) {
          st.current.tx = d.tx0 + d.dx;
          st.current.ty = d.ty0 + d.dy;
          clamp();
          paint();
        } else if (el.current) {
          // Its own size: the photo gives a little with the finger, and goes
          // back or on when it lifts.
          el.current.style.transition = "none";
          el.current.style.transform = `translate(${d.dx * 0.6}px, ${d.dy * 0.6}px)`;
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
          if (d.moved) {
            paint(true);
            if (Math.abs(d.dx) > FLICK_X && Math.abs(d.dx) > Math.abs(d.dy)) onFlick(d.dx < 0 ? "next" : "prev");
            else if (d.dy > FLICK_Y && Math.abs(d.dy) > Math.abs(d.dx)) onFlick("close");
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
