"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { X } from "lucide-react";
import { handleWorkerMessage, onPushShown, type ShownPush } from "@/lib/resume";

/**
 * A push that arrives while the console is on screen, shown inside it: the
 * phone's own banner is easy to miss with the app in front, and the screen
 * behind it has already re-read (lib/resume.ts). Tapping it goes where the
 * push points; it leaves on its own after a few seconds.
 */
export function PushToast() {
  const router = useRouter();
  const [push, setPush] = useState<ShownPush | null>(null);
  const [more, setMore] = useState(0);
  const timer = useRef<number | undefined>(undefined);
  const showing = useRef(false);

  useEffect(
    () =>
      onPushShown((p) => {
        // One at a time: a second push while one is up replaces it and is
        // counted, rather than stacking banners over the screen.
        setMore((m) => (showing.current ? m + 1 : 0));
        showing.current = true;
        setPush(p);
        window.clearTimeout(timer.current);
        timer.current = window.setTimeout(() => {
          showing.current = false;
          setPush(null);
        }, 7000);
      }),
    [],
  );
  useEffect(() => () => window.clearTimeout(timer.current), []);

  if (!push) return null;
  const dismiss = () => {
    window.clearTimeout(timer.current);
    showing.current = false;
    setPush(null);
  };
  // Same as tapping the phone's own notification.
  const open = () => {
    dismiss();
    handleWorkerMessage({ type: "jarvis:open", url: push.url }, (path) => router.push(path));
  };

  return (
    <div
      role="status"
      aria-live="polite"
      className="push-toast fixed inset-x-0 z-50 mx-auto flex w-[calc(100%-32px)] max-w-[26rem] items-start rounded-2xl border border-edge bg-glass-raised shadow-lg backdrop-blur-xl"
      style={{ top: "calc(env(safe-area-inset-top) + 8px)" }}
    >
      <button type="button" onClick={open} className="min-w-0 flex-1 px-4 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 rounded-2xl">
        <span className="block text-[14px] font-semibold text-foreground">
          {push.title}
          {more ? <span className="tnum ml-1.5 text-[12px] font-normal text-faint">외 {more}건</span> : null}
        </span>
        {push.body ? <span className="mt-0.5 line-clamp-2 block text-[13px] leading-snug text-dim">{push.body}</span> : null}
      </button>
      <button
        type="button"
        onClick={dismiss}
        aria-label="닫기"
        className="flex size-11 shrink-0 items-center justify-center rounded-xl text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <X aria-hidden className="size-4" />
      </button>
    </div>
  );
}
