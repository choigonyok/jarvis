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
const FOCUS = "jarvis:focus-proposal";

/** A tapped push about one card: the thread scrolls to it (chat/thread.tsx). */
export function onFocusProposal(fn: (id: string) => void): () => void {
  const handle = (e: Event) => fn((e as CustomEvent<string>).detail);
  window.addEventListener(FOCUS, handle);
  return () => window.removeEventListener(FOCUS, handle);
}

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
    // A push that reached the phone: the phone showed it, so the page only
    // re-reads. What the page shows itself comes over its own stream
    // (useLiveNotifications), for devices notify-svc knows are looking.
    window.dispatchEvent(new Event(RESUME));
    return;
  }
  if (msg?.type !== "jarvis:open") return;
  const target = new URL(String(msg.url || "/"), window.location.origin);
  window.dispatchEvent(new Event(RESUME));
  if (target.searchParams.get("inbox") === "1") window.dispatchEvent(new Event(INBOX));
  if (target.pathname !== window.location.pathname) {
    // Another screen: it reads ?proposal= itself once it mounts.
    go(target.pathname + target.search);
    return;
  }
  const card = target.searchParams.get("proposal");
  if (card) window.dispatchEvent(new CustomEvent<string>(FOCUS, { detail: card }));
}

/** Show a push in the page and re-read, as one that came over the stream. */
export function showInApp(p: ShownPush) {
  window.dispatchEvent(new Event(RESUME));
  window.dispatchEvent(new CustomEvent<ShownPush>(SHOWN, { detail: p }));
}

/**
 * While the console is on screen: an open stream from notify-svc, and every
 * 10 seconds a word that this device is looking - so notify-svc sends what
 * happens over the stream, shown in the page, instead of a phone push whose
 * banner would cover it. Leaving the screen closes the stream and says so;
 * a phone that suspends the page without a word stops saying it, and
 * notify-svc goes back to pushing 15 seconds later (internal/live).
 */
export function useLiveNotifications(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    let source: EventSource | null = null;
    let ping: number | undefined;
    let endpoint = "";
    const presence = (visible: boolean) => {
      if (!endpoint) return;
      const body = JSON.stringify({ endpoint, visible });
      if (!visible && navigator.sendBeacon) {
        navigator.sendBeacon("/api/notify/presence", new Blob([body], { type: "application/json" }));
        return;
      }
      void fetch("/api/notify/presence", { method: "POST", headers: { "content-type": "application/json" }, body, keepalive: true }).catch(() => {});
    };
    const start = () => {
      if (source) return;
      source = new EventSource("/api/notify/stream");
      source.onmessage = (e) => {
        try {
          const p = JSON.parse(e.data) as ShownPush;
          showInApp({ title: p.title || "jarvis", body: p.body, url: p.url });
        } catch {
          /* 해석할 수 없는 프레임은 버린다 */
        }
      };
      // Only a device whose stream is open may say it is looking: one that
      // said so with a broken stream would get neither a push nor a banner.
      const say = () => source?.readyState === EventSource.OPEN && presence(true);
      source.onopen = say;
      ping = window.setInterval(say, 10_000);
    };
    const stop = () => {
      window.clearInterval(ping);
      source?.close();
      source = null;
      presence(false);
    };
    const onVisibility = () => (document.visibilityState === "visible" ? start() : stop());

    // The push subscription's endpoint is what notify-svc knows this device by.
    void (async () => {
      try {
        const reg = await navigator.serviceWorker?.getRegistration();
        endpoint = (await reg?.pushManager.getSubscription())?.endpoint ?? "";
      } catch {
        endpoint = "";
      }
      if (document.visibilityState === "visible") start();
    })();
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", stop);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", stop);
      stop();
    };
  }, [enabled]);
}

export function useNotificationOpen(router: ReturnType<typeof useRouter>) {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const onMessage = (e: MessageEvent) => handleWorkerMessage(e.data, (path) => router.push(path));
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [router]);
}
