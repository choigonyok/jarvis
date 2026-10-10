"use client";

import { useEffect } from "react";
import type { useRouter } from "next/navigation";

/**
 * Coming back to the console. A phone suspends a home-screen app in the
 * background and quietly kills its connections - without an error, so the
 * page cannot tell its stream is dead and keeps showing what it had. Each
 * screen re-reads when this fires: the page becoming visible again, a page
 * restored from the back-forward cache, or a tapped notification (which may
 * bring forward a window that was never hidden, on a desktop).
 */
const RESUME = "jarvis:resume";
const INBOX = "jarvis:inbox";

export function onResume(fn: () => void): () => void {
  const visible = () => document.visibilityState === "visible" && fn();
  const shown = (e: PageTransitionEvent) => e.persisted && fn();
  document.addEventListener("visibilitychange", visible);
  window.addEventListener("pageshow", shown);
  window.addEventListener(RESUME, fn);
  return () => {
    document.removeEventListener("visibilitychange", visible);
    window.removeEventListener("pageshow", shown);
    window.removeEventListener(RESUME, fn);
  };
}

/** The inbox opens when a grouped push ("밤사이 알림 3건") is tapped. */
export function onInboxRequest(fn: () => void): () => void {
  window.addEventListener(INBOX, fn);
  return () => window.removeEventListener(INBOX, fn);
}

/**
 * A tapped notification, as the service worker reports it (public/sw.js):
 * re-read everything, and go to the screen it is about if that is not the
 * one showing.
 */
export function useNotificationOpen(router: ReturnType<typeof useRouter>) {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const onMessage = (e: MessageEvent) => {
      if (e.data?.type !== "jarvis:open") return;
      const target = new URL(String(e.data.url || "/"), window.location.origin);
      window.dispatchEvent(new Event(RESUME));
      if (target.searchParams.get("inbox") === "1") window.dispatchEvent(new Event(INBOX));
      if (target.pathname !== window.location.pathname) router.push(target.pathname + target.search);
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [router]);
}
