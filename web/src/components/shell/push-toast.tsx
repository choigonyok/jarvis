"use client";

import { useEffect, useRef } from "react";
import { usePathname, useRouter } from "next/navigation";
import { toast } from "sonner";
import { useRole } from "@/components/shell/role";
import { PUSH_TOASTER } from "@/components/shell/toasts";
import { readable } from "@/lib/brief";
import { handleWorkerMessage, onPushShown, useLiveNotifications, useNotificationOpen, type ShownPush } from "@/lib/resume";

const SHOWN_MS = 4000;

/**
 * Pushes while the console is on screen, on every tab: the service worker's
 * messages are heard here (lib/resume.ts), and a push that arrives drops a
 * banner in from above the screen, holds for a few seconds and goes back up.
 * Mounted once, in the root layout, beside the toasters (shell/toasts.tsx).
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

/** After a finger lets go of the banner without opening it, how long it stays. */
const LET_GO_MS = 2000;

/**
 * The banner: a toast on the push toaster (shell/toasts.tsx). One at a time -
 * a second push while one is up takes its place and is counted ("외 2건"),
 * rather than stacking over the screen. A tap goes where the push points; a
 * flick up sends it away early (sonner's swipe, toward the edge it came from);
 * a finger on it holds it.
 *
 * Its time is kept here, not by sonner: sonner stops the clock while a mouse
 * is over the toaster and starts it when the mouse leaves, and under a finger
 * nothing ever leaves - a touch that did not end in a tap (iOS takes some for
 * a hover) left the banner up for good, over the header's controls. So the
 * clock stops only while a finger is actually down on it.
 */
function PushToast() {
  const router = useRouter();
  const showing = useRef(false);
  const more = useRef(0);
  const clock = useRef<number | undefined>(undefined);

  useEffect(() => {
    const away = (after: number) => {
      window.clearTimeout(clock.current);
      clock.current = window.setTimeout(() => toast.dismiss("push"), after);
    };
    const off = onPushShown((p) => {
      more.current = showing.current ? more.current + 1 : 0;
      showing.current = true;
      const count = more.current;
      const gone = () => {
        showing.current = false;
        window.clearTimeout(clock.current);
      };
      toast.custom(
        (id) => (
          <Banner
            push={p}
            more={count}
            onHold={() => window.clearTimeout(clock.current)}
            onLetGo={() => away(LET_GO_MS)}
            onOpen={() => {
              toast.dismiss(id);
              handleWorkerMessage({ type: "jarvis:open", url: p.url }, (path) => router.push(path));
            }}
          />
        ),
        { id: "push", toasterId: PUSH_TOASTER, duration: Infinity, onDismiss: gone },
      );
      away(SHOWN_MS);
    });
    return () => {
      off();
      window.clearTimeout(clock.current);
    };
  }, [router]);

  return null;
}

function Banner({
  push,
  more,
  onOpen,
  onHold,
  onLetGo,
}: {
  push: ShownPush;
  more: number;
  onOpen: () => void;
  /** A finger is down on it: it stays. */
  onHold: () => void;
  /** The finger went without opening it: it goes a little later. */
  onLetGo: () => void;
}) {
  const said = readable(push.title, push.body ?? "");
  return (
    // A drag on the banner is the banner's: not the page's pull to refresh.
    <div
      data-no-pull=""
      className="w-full select-none"
      role="status"
      aria-live="polite"
      onPointerDown={onHold}
      onPointerUp={onLetGo}
      onPointerCancel={onLetGo}
    >
      <button
        type="button"
        onClick={onOpen}
        className="liquid-glass block w-full rounded-2xl px-4 py-3 text-left focus-ring"
      >
        <span className="line-clamp-1 text-body font-semibold text-foreground">
          {said.title}
          {more ? <span className="tnum ml-1.5 text-aside font-normal text-faint">외 {more}건</span> : null}
        </span>
        {said.body ? <span className="mt-0.5 line-clamp-2 text-ui leading-snug text-dim">{said.body}</span> : null}
      </button>
    </div>
  );
}
