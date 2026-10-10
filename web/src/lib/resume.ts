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
const SHOWN = "jarvis:push-shown";

/** A push as the service worker relays it to an open console. */
export type ShownPush = { title: string; body?: string; url?: string };

export function onPushShown(fn: (p: ShownPush) => void): () => void {
  const handle = (e: Event) => fn((e as CustomEvent<ShownPush>).detail);
  window.addEventListener(SHOWN, handle);
  return () => window.removeEventListener(SHOWN, handle);
}

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
 * What the service worker tells an open console (public/sw.js). A push that
 * just arrived: re-read now - the inbox, the cards - and show it in the app
 * if the app is on screen. A tapped notification: re-read, and go to the
 * screen it is about if that is not the one showing.
 */
export function handleWorkerMessage(data: unknown, go: (path: string) => void) {
  const msg = data as { type?: string; title?: string; body?: string; url?: string } | null;
  if (msg?.type === "jarvis:push") {
    window.dispatchEvent(new Event(RESUME));
    if (document.visibilityState === "visible") {
      const detail: ShownPush = { title: msg.title || "jarvis", body: msg.body, url: msg.url };
      window.dispatchEvent(new CustomEvent<ShownPush>(SHOWN, { detail }));
    }
    return;
  }
  if (msg?.type !== "jarvis:open") return;
  const target = new URL(String(msg.url || "/"), window.location.origin);
  window.dispatchEvent(new Event(RESUME));
  if (target.searchParams.get("inbox") === "1") window.dispatchEvent(new Event(INBOX));
  if (target.pathname !== window.location.pathname) go(target.pathname + target.search);
}

export function useNotificationOpen(router: ReturnType<typeof useRouter>) {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const onMessage = (e: MessageEvent) => handleWorkerMessage(e.data, (path) => router.push(path));
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [router]);
}
