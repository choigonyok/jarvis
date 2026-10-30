"use client";

import { useEffect } from "react";
import type { useRouter } from "next/navigation";
import { openTarget } from "@/lib/focus";

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

/** A re-read that answers with a promise lets a pull to refresh wait for it. */
type Wait = (done: Promise<unknown>) => void;

export function onResume(fn: () => unknown): () => void {
  const visible = () => document.visibilityState === "visible" && fn();
  const shown = (e: PageTransitionEvent) => e.persisted && fn();
  const asked = (e: Event) => {
    const done = fn();
    const wait = (e as CustomEvent<{ wait?: Wait } | null>).detail?.wait;
    if (wait && done instanceof Promise) wait(done);
  };
  document.addEventListener("visibilitychange", visible);
  window.addEventListener("pageshow", shown);
  window.addEventListener(RESUME, asked);
  return () => {
    document.removeEventListener("visibilitychange", visible);
    window.removeEventListener("pageshow", shown);
    window.removeEventListener(RESUME, asked);
  };
}

/** Asks every screen to re-read and reopen its streams, as on coming back to the app. */
export function requestResume() {
  window.dispatchEvent(new Event(RESUME));
}

/**
 * Pull to refresh: every screen re-reads, as on coming back to the app, and
 * this resolves once what they started has come back - or after a while, so
 * a service that hangs does not hold the spinner up.
 */
export function refreshAll(timeout = 8000): Promise<void> {
  const started: Promise<unknown>[] = [];
  window.dispatchEvent(new CustomEvent(RESUME, { detail: { wait: (done: Promise<unknown>) => started.push(done) } }));
  return Promise.race([
    Promise.allSettled(started).then(() => undefined),
    new Promise<void>((resolve) => setTimeout(resolve, timeout)),
  ]);
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
    // (useLiveNotifications), for devices output-svc knows are looking.
    window.dispatchEvent(new Event(RESUME));
    return;
  }
  if (msg?.type !== "jarvis:open") return;
  const target = new URL(String(msg.url || "/"), window.location.origin);
  window.dispatchEvent(new Event(RESUME));
  if (target.searchParams.get("inbox") === "1") window.dispatchEvent(new Event(INBOX));
  const card = target.searchParams.get("proposal");
  if (card && target.pathname === window.location.pathname) {
    window.dispatchEvent(new CustomEvent<string>(FOCUS, { detail: card }));
    return;
  }
  // Another screen reads ?proposal= / ?focus= itself once it mounts; this
  // one is told (lib/focus.ts).
  openTarget(target.pathname + target.search, go);
}

/** Show a push in the page and re-read, as one that came over the stream. */
export function showInApp(p: ShownPush) {
  window.dispatchEvent(new Event(RESUME));
  window.dispatchEvent(new CustomEvent<ShownPush>(SHOWN, { detail: p }));
}

/**
 * While the console is on screen: an open stream from output-svc, and every
 * 10 seconds a word that this device is looking - so output-svc sends what
 * happens over the stream, shown in the page, instead of a phone push whose
 * banner would cover it. Leaving the screen closes the stream and says so;
 * a phone that suspends the page without a word stops saying it, and
 * output-svc goes back to pushing 15 seconds later (internal/live).
 */
export function useLiveNotifications(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    let source: EventSource | null = null;
    let ping: number | undefined;
    let endpoint = "";
    // A refused reconnect, opened again by hand (see onerror), and how long the next waits.
    let again: number | undefined;
    let wait = 0;
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
      source.onopen = () => {
        wait = 0;
        say();
      };
      // A reconnect answered with anything but a stream closes it for good:
      // the page stops saying it is looking, and output-svc goes back to
      // phone pushes - banners over the open app - until it is next hidden
      // and shown. Opened again here instead, while the page is on screen.
      source.onerror = () => {
        if (source?.readyState !== EventSource.CLOSED) return;
        window.clearInterval(ping);
        source = null;
        wait = Math.min(wait ? wait * 2 : 1000, 15_000);
        window.clearTimeout(again);
        again = window.setTimeout(() => document.visibilityState === "visible" && start(), wait);
      };
      ping = window.setInterval(say, 10_000);
    };
    const stop = () => {
      window.clearInterval(ping);
      window.clearTimeout(again);
      source?.close();
      source = null;
      presence(false);
    };
    const onVisibility = () => (document.visibilityState === "visible" ? start() : stop());

    // The push subscription's endpoint is what output-svc knows this device by.
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
