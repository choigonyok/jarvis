// The console's service worker: it exists for Web Push. notify-svc sends a
// small JSON payload ({title, body, url, tag}); this shows it, and a tap opens
// the console on the screen it is about - an open window if there is one.
//
// Nothing is cached here: the console is live data behind Cloudflare Access,
// and a stale copy served offline would be wrong in a way that looks right.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = { title: "jarvis", body: event.data ? event.data.text() : "" };
  }
  const title = data.title || "jarvis";
  event.waitUntil(
    (async () => {
      // Always shown: a push that shows nothing is what gets a site's push
      // permission withdrawn (iOS counts them).
      await self.registration.showNotification(title, {
        body: data.body || "",
        tag: data.tag || undefined,
        // A replaced notification with the same tag still deserves a buzz.
        renotify: Boolean(data.tag),
        icon: "/icons/icon-192.v2.png",
        badge: "/icons/icon-192.v2.png",
        data: { url: data.url || "/" },
      });
      // And an open console hears of it at once: it re-reads, and shows it
      // in the app if it is on screen (lib/resume.ts).
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const w of windows) {
        w.postMessage({ type: "jarvis:push", title, body: data.body || "", url: data.url || "/" });
      }
    })(),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL(event.notification.data?.url || "/", self.location.origin).href;
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const w of windows) {
        if (new URL(w.url).origin === self.location.origin) {
          // The open console re-reads and moves to the screen itself
          // (lib/resume.ts). navigate() would reload the whole app - and a
          // home-screen app on iOS does not reliably do even that, so the
          // tap used to show what the page had before it was suspended.
          // Refused focus (a browser that allows it only for some taps)
          // must not cost the re-read.
          await w.focus().catch(() => {});
          w.postMessage({ type: "jarvis:open", url });
          return;
        }
      }
      return self.clients.openWindow(url);
    })(),
  );
});

// The push service rotated this device's subscription: register the new one.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(
    (async () => {
      const res = await fetch("/api/notify/vapid", { credentials: "include" });
      if (!res.ok) return;
      const { publicKey } = await res.json();
      const sub = await self.registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: publicKey,
      });
      await fetch("/api/notify/subscriptions", {
        method: "POST",
        credentials: "include",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(sub.toJSON()),
      });
    })(),
  );
});
