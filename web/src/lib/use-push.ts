"use client";

import { useCallback, useEffect, useState } from "react";

/**
 * Web Push for this device: register the service worker (public/sw.js),
 * subscribe with notify-svc's public key, and hand the subscription to
 * notify-svc. From then on a notification arrives with the console closed -
 * which the in-page Notification it replaces could never do on a phone.
 *
 * iOS delivers Web Push only to a console added to the home screen (16.4+);
 * in a Safari tab there is no PushManager, and that is said rather than hidden.
 */

export type PushState = "loading" | "unsupported" | "needs-home-screen" | "off" | "on" | "denied" | "unavailable";

const supported = () =>
  typeof window !== "undefined" && "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;

const isIOS = () => typeof navigator !== "undefined" && /iPhone|iPad|iPod/.test(navigator.userAgent);

/** The key arrives base64url; PushManager wants bytes. */
function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function registration(): Promise<ServiceWorkerRegistration> {
  return navigator.serviceWorker.register("/sw.js", { scope: "/" });
}

export function usePush() {
  const [state, setState] = useState<PushState>("loading");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    void (async () => {
      if (!supported()) {
        if (alive) setState(isIOS() ? "needs-home-screen" : "unsupported");
        return;
      }
      if (Notification.permission === "denied") {
        if (alive) setState("denied");
        return;
      }
      try {
        const reg = await registration();
        const sub = await reg.pushManager.getSubscription();
        if (alive) setState(sub ? "on" : "off");
      } catch {
        if (alive) setState("unavailable");
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  const enable = useCallback(async () => {
    setBusy(true);
    try {
      const permission = await Notification.requestPermission();
      if (permission !== "granted") {
        setState(permission === "denied" ? "denied" : "off");
        return;
      }
      const keyRes = await fetch("/api/notify/vapid", { cache: "no-store" });
      const key = (await keyRes.json()) as { publicKey?: string; ready?: boolean };
      if (!keyRes.ok || !key.publicKey) {
        setState("unavailable");
        return;
      }
      const reg = await registration();
      const sub =
        (await reg.pushManager.getSubscription()) ??
        (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(key.publicKey) }));
      const res = await fetch("/api/notify/subscriptions", {
        method: "POST",
        headers: { "content-type": "application/json", "x-client-user-agent": navigator.userAgent },
        body: JSON.stringify(sub.toJSON()),
      });
      setState(res.ok ? "on" : "unavailable");
    } catch {
      setState("unavailable");
    } finally {
      setBusy(false);
    }
  }, []);

  const disable = useCallback(async () => {
    setBusy(true);
    try {
      const reg = await registration();
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        await fetch("/api/notify/subscriptions", {
          method: "DELETE",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ endpoint: sub.endpoint }),
        }).catch(() => {});
        await sub.unsubscribe();
      }
      setState("off");
    } finally {
      setBusy(false);
    }
  }, []);

  return { state, busy, enable, disable };
}
