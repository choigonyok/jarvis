"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { Proposal } from "@/lib/thread";
import { summarize } from "@/lib/ledger";

/**
 * A request waiting on you is worth a notification even when the tab is not
 * the one you are looking at - that is what makes this a pager rather than a
 * page. The browser's own Notification API does it: free, no key, no service
 * to sign up to, and it works on a phone once the console is on the home
 * screen.
 *
 * Off until asked for. A permission prompt on first load is the fastest way
 * to be denied for good, so the bar offers it in words first.
 */

const KEY = "jarvis:notify";

export type NotifyState = "unsupported" | "off" | "on" | "denied";

export function useNotify(pending: Proposal[]) {
  const [state, setState] = useState<NotifyState>("off");
  // Whatever was already waiting when the page loaded is not news.
  const announced = useRef<Set<string> | null>(null);

  useEffect(() => {
    if (typeof window === "undefined" || !("Notification" in window)) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setState("unsupported");
      return;
    }
    const wanted = window.localStorage.getItem(KEY) === "on";
    setState(
      Notification.permission === "denied"
        ? "denied"
        : wanted && Notification.permission === "granted"
          ? "on"
          : "off",
    );
  }, []);

  const enable = useCallback(async () => {
    if (!("Notification" in window)) return;
    const permission =
      Notification.permission === "granted"
        ? "granted"
        : await Notification.requestPermission();
    if (permission !== "granted") {
      setState(permission === "denied" ? "denied" : "off");
      return;
    }
    window.localStorage.setItem(KEY, "on");
    setState("on");
  }, []);

  const disable = useCallback(() => {
    // The browser gives no way to hand a permission back, so this is ours to
    // remember: granted but not wanted.
    window.localStorage.removeItem(KEY);
    setState("off");
  }, []);

  useEffect(() => {
    if (state !== "on") return;

    if (announced.current === null) {
      announced.current = new Set(pending.map((p) => p.id));
      return;
    }

    for (const proposal of pending) {
      if (announced.current.has(proposal.id)) continue;
      announced.current.add(proposal.id);
      // Looking at the page already is being told.
      if (document.visibilityState === "visible") continue;
      try {
        const note = new Notification("결재 대기", {
          body: summarize(proposal),
          // Same tag per proposal: a reconnect that re-delivers the frame
          // replaces the notification instead of stacking a second one.
          tag: proposal.id,
          // It is waiting until decided; it should not fade off the shade.
          requireInteraction: true,
        });
        note.onclick = () => {
          window.focus();
          note.close();
        };
      } catch {
        /* 알림을 띄우지 못해도 대기 줄은 그대로 서 있다 */
      }
    }

    // A request decided elsewhere should stop being remembered as announced.
    const live = new Set(pending.map((p) => p.id));
    for (const id of announced.current) if (!live.has(id)) announced.current.delete(id);
  }, [state, pending]);

  return { state, enable, disable };
}
