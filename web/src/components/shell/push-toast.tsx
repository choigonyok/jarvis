"use client";

import { useEffect, useRef, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useRole } from "@/components/shell/role";
import { handleWorkerMessage, onPushShown, useLiveNotifications, useNotificationOpen, type ShownPush } from "@/lib/resume";

const SHOWN_MS = 4000;
const LEAVE_MS = 240;

/**
 * Pushes while the console is on screen, on every tab: the service worker's
 * messages are heard here (lib/resume.ts), and a push that arrives drops a
 * banner in from above the screen, holds for a few seconds and goes back up.
 * A tap goes where the push points; flicking it up sends it away early.
 * Mounted once, in the root layout.
 */
export function PushBridge() {
  const role = useRole();
  const router = useRouter();
  useNotificationOpen(router);
  // Not on /login: nobody is signed in to stream to there.
  const signedIn = !usePathname().startsWith("/login");
  useLiveNotifications(role === "owner" && signedIn);
  return role === "owner" ? <PushToast /> : null;
}

function PushToast() {
  const router = useRouter();
  const [push, setPush] = useState<ShownPush | null>(null);
  const [leaving, setLeaving] = useState(false);
  const [more, setMore] = useState(0);
  const showing = useRef(false);
  const timer = useRef<number | undefined>(undefined);
  const drag = useRef<{ y: number; dy: number } | null>(null);
  const box = useRef<HTMLDivElement>(null);

  const leave = () => {
    window.clearTimeout(timer.current);
    setLeaving(true);
    timer.current = window.setTimeout(() => {
      showing.current = false;
      setPush(null);
      setLeaving(false);
    }, LEAVE_MS);
  };
  const leaveRef = useRef(leave);
  useEffect(() => {
    leaveRef.current = leave;
  });

  useEffect(
    () =>
      onPushShown((p) => {
        // One banner at a time: a second push while one is up replaces it
        // and is counted, rather than stacking over the screen.
        setMore((m) => (showing.current ? m + 1 : 0));
        showing.current = true;
        setPush(p);
        setLeaving(false);
        window.clearTimeout(timer.current);
        timer.current = window.setTimeout(() => leaveRef.current(), SHOWN_MS);
      }),
    [],
  );
  useEffect(() => () => window.clearTimeout(timer.current), []);

  if (!push) return null;

  const open = () => {
    leave();
    handleWorkerMessage({ type: "jarvis:open", url: push.url }, (path) => router.push(path));
  };

  return (
    <div
      ref={box}
      role="status"
      aria-live="polite"
      // A drag on the banner is the banner's: not the page's pull to refresh.
      data-no-pull=""
      className={`push-toast fixed inset-x-0 z-50 mx-auto w-[calc(100%-32px)] max-w-[26rem] touch-none select-none ${leaving ? "push-toast-leave" : ""}`}
      style={{ top: "calc(env(safe-area-inset-top) + 8px)" }}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        drag.current = { y: e.clientY, dy: 0 };
        window.clearTimeout(timer.current); // held while touched
      }}
      onPointerMove={(e) => {
        if (!drag.current || !box.current) return;
        drag.current.dy = Math.min(0, e.clientY - drag.current.y);
        box.current.style.transform = `translateY(${drag.current.dy}px)`;
      }}
      onPointerUp={() => {
        const d = drag.current;
        drag.current = null;
        if (!d || !box.current) return;
        if (d.dy < -24) {
          // Flicked up: it carries on from where the finger let go. Put back
          // to its place first, the exit would start from there - a jump
          // down and a second rise, which looked like a second banner.
          box.current.style.setProperty("--drag", `${d.dy}px`);
          return leave();
        }
        box.current.style.transform = "";
        if (d.dy > -6) return open(); // a tap
        timer.current = window.setTimeout(() => leaveRef.current(), SHOWN_MS);
      }}
      onPointerCancel={() => {
        drag.current = null;
        if (box.current) box.current.style.transform = "";
        timer.current = window.setTimeout(() => leaveRef.current(), SHOWN_MS);
      }}
    >
      <button
        type="button"
        onClick={(e) => e.preventDefault()}
        onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && open()}
        className="block w-full rounded-2xl border border-edge bg-glass-raised px-4 py-3 text-left shadow-lg backdrop-blur-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <span className="block text-[14px] font-semibold text-foreground">
          {push.title}
          {more ? <span className="tnum ml-1.5 text-[12px] font-normal text-faint">외 {more}건</span> : null}
        </span>
        {push.body ? <span className="mt-0.5 line-clamp-2 block text-[13px] leading-snug text-dim">{push.body}</span> : null}
      </button>
    </div>
  );
}
