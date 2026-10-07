"use client";

import { useEffect } from "react";

/**
 * Locks zoom in the home-screen app only. Run from the home screen, this is an
 * app, and a pinch or a double tap that zooms the whole shell - tab bar and
 * all - reads as something broken. In the browser zoom stays available (see
 * the viewport note in app/layout.tsx).
 *
 * iOS does not reliably honour user-scalable=no on its own, so the meta tag is
 * backed by cancelling the pinch itself: Safari's gesture events, and any
 * touch move with more than one finger.
 */
export function NoZoom() {
  useEffect(() => {
    const standalone =
      window.matchMedia("(display-mode: standalone)").matches ||
      (navigator as Navigator & { standalone?: boolean }).standalone === true;
    if (!standalone) return;

    const meta = document.querySelector<HTMLMetaElement>('meta[name="viewport"]');
    const before = meta?.content;
    if (meta) {
      meta.content = before!
        .split(",")
        .map((s) => s.trim())
        .filter((s) => !/^(maximum-scale|minimum-scale|user-scalable)=/.test(s))
        .concat("maximum-scale=1", "minimum-scale=1", "user-scalable=no")
        .join(", ");
    }

    const stop = (e: Event) => e.preventDefault();
    const pinch = (e: TouchEvent) => {
      if (e.touches.length > 1) e.preventDefault();
    };
    document.addEventListener("gesturestart", stop, { passive: false });
    document.addEventListener("gesturechange", stop, { passive: false });
    document.addEventListener("touchmove", pinch, { passive: false });
    document.documentElement.classList.add("no-zoom");
    return () => {
      if (meta && before) meta.content = before;
      document.removeEventListener("gesturestart", stop);
      document.removeEventListener("gesturechange", stop);
      document.removeEventListener("touchmove", pinch);
      document.documentElement.classList.remove("no-zoom");
    };
  }, []);
  return null;
}
