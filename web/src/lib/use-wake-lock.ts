"use client";

import { useEffect } from "react";

/**
 * Keep the screen on while `active`: a screen used without a hand on it - a
 * workout between sets, voice mode with the phone stood up while Jarvis
 * thinks and answers.
 *
 * The lock is dropped by the browser when the tab is hidden, so it is asked
 * for again on the way back, and let go of once `active` ends.
 */
export function useWakeLock(active: boolean) {
  useEffect(() => {
    if (!active || !("wakeLock" in navigator)) return;
    let lock: WakeLockSentinel | null = null;
    let cancelled = false;
    const take = async () => {
      if (document.visibilityState !== "visible") return;
      try {
        const next = await navigator.wakeLock.request("screen");
        if (cancelled) void next.release();
        else lock = next;
      } catch {
        // Low battery mode or a denied request: the screen just sleeps as usual.
      }
    };
    void take();
    document.addEventListener("visibilitychange", take);
    return () => {
      cancelled = true;
      document.removeEventListener("visibilitychange", take);
      void lock?.release();
    };
  }, [active]);
}
