"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { useThread } from "@/lib/use-thread";
import { isPending } from "@/lib/thread";
import { cn } from "@/lib/utils";

type State = "connecting" | "open" | "closed";

/**
 * What the agent is looking at.
 *
 * Not a VNC client - the product has no use for one. The browser tools skip
 * the approval card, so this screen is the only thing left that can catch
 * them; its job is witnessing, and every choice below follows from that.
 *
 * Watching is the default and it is passive: input is off until the operator
 * takes it. Taking control is the one loud moment on this surface, because it
 * is the one moment the operator is no longer only watching.
 */
export function Screen({ vncUrl }: { vncUrl: string }) {
  const { proposals, connection } = useThread();
  const waitingList = proposals.filter(isPending);
  const pending = waitingList.length;

  const mount = useRef<HTMLDivElement>(null);
  const rfb = useRef<{ viewOnly: boolean; disconnect: () => void } | null>(null);
  const [state, setState] = useState<State>("connecting");
  const [controlling, setControlling] = useState(false);

  useEffect(() => {
    const target = mount.current;
    if (!target) return;

    let live = true;
    let client: { viewOnly: boolean; disconnect: () => void } | null = null;

    // noVNC touches WebSocket and the DOM on import, so it cannot be part of
    // the server bundle.
    import("@novnc/novnc").then(({ default: RFB }) => {
      if (!live) return;

      const instance = new RFB(target, vncUrl || defaultURL(), {});
      // Scale to whatever width the layout gives us; never resize the remote
      // screen, because the agent's session should not change shape when a
      // person happens to be looking at it.
      instance.scaleViewport = true;
      instance.resizeSession = false;
      instance.viewOnly = true;
      // The canvas sits in a well that already has a colour; noVNC painting
      // its own grey behind it would show as a second surface.
      instance.background = "transparent";

      instance.addEventListener("connect", () => setState("open"));
      instance.addEventListener("disconnect", () => {
        setState("closed");
        setControlling(false);
      });

      client = instance;
      rfb.current = instance;
    });

    return () => {
      live = false;
      client?.disconnect();
      rfb.current = null;
    };
  }, [vncUrl]);

  const toggleControl = useCallback(() => {
    setControlling((on) => {
      const next = !on;
      if (rfb.current) rfb.current.viewOnly = !next;
      return next;
    });
  }, []);

  // Escape is how you get out of anything that has taken over a screen.
  useEffect(() => {
    if (!controlling) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") toggleControl();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [controlling, toggleControl]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div
        className={cn(
          "shrink-0 transition-opacity duration-200 motion-reduce:transition-none",
          controlling && "opacity-25",
        )}
      >
        <Header connection={connection} pending={pending} />
        {/* Inside the dimmed block on purpose: while you are driving the
            remote browser the room goes quiet, and that includes this. */}
        <StandingBar pending={waitingList} href="/record" />
      </div>

      <main
        className={cn(
          "inset-x-safe min-h-0 flex-1",
          controlling
            ? "pb-safe fixed inset-0 z-50 flex flex-col bg-background/95 p-3 backdrop-blur-sm sm:p-6"
            : "mx-auto flex w-full max-w-[46rem] flex-col px-4 py-3 sm:px-8 sm:py-4",
        )}
      >
        <div className="mb-2.5 flex shrink-0 items-center justify-between gap-3">
          {/* The empty states speak inside the well; saying it twice here
              would be the same sentence in two places.

              Esc is the desktop way out. A phone has no Esc, so the button on
              the right is the only exit there - which is why it is named for
              leaving rather than toggled silently. */}
          <p className="min-w-0 truncate text-[11.5px] text-faint" aria-live="polite">
            {controlling ? (
              <>
                조작 중<span className="hidden sm:inline"> · Esc로 돌아갑니다</span>
              </>
            ) : (
              ""
            )}
          </p>
          <button
            type="button"
            onClick={toggleControl}
            disabled={state !== "open"}
            aria-pressed={controlling}
            className={cn(
              "flex shrink-0 items-center justify-center rounded-lg px-3 text-[12.5px] transition-colors outline-none",
              "min-h-11 sm:min-h-0 sm:px-2 sm:py-1 sm:text-[12px]",
              "focus-visible:ring-3 focus-visible:ring-ring/50",
              "disabled:pointer-events-none disabled:text-faint/40",
              controlling
                ? "bg-glass-raised text-foreground"
                : "text-faint hover:text-dim",
            )}
          >
            {controlling ? "보기로 돌아가기" : "조작"}
          </button>
        </div>

        {/* No border: in this product a border means a decision is waiting,
            and this is a window, not a card. It is recessed instead.

            The remote screen is 16:10 and this is a window onto it, so where
            there is width to spare the well keeps that shape rather than
            stretching to whatever height is going - a hole the size of the
            page reads as breakage. A portrait phone has no width to spare,
            and holding 16:10 there would leave a 190px letterbox, so the
            window takes the height instead.

            Watching is this surface's job, so what is being watched is never
            dimmed. Taking control is marked by the room going quiet around
            it, not by the screen being held back until then. */}
        <div
          className={cn(
            "relative overflow-hidden rounded-lg bg-well",
            controlling
              ? "min-h-0 flex-1"
              : "min-h-0 w-full flex-1 sm:aspect-[16/10] sm:flex-none",
          )}
        >
          <div ref={mount} className="absolute inset-0" />

          {state !== "open" && (
            <div className="absolute inset-0 grid place-items-center px-8">
              <p className="max-w-[24rem] text-center text-[12.5px] leading-relaxed text-balance text-faint sm:text-[13px]">
                {state === "connecting" ? (
                  "에이전트가 브라우저를 쓰기 시작하면 여기에 나타납니다."
                ) : (
                  <>
                    브라우저 컨테이너가 응답하지 않습니다.
                    <br />
                    <code className="mt-2 inline-block font-mono text-[12px] text-dim">
                      docker compose up -d browser
                    </code>
                  </>
                )}
              </p>
            </div>
          )}
        </div>
      </main>

      {/* Hidden while controlling: that mode is already fullscreen, and a nav
          bar under a screen you are driving is a mis-tap waiting to happen. */}
      {controlling ? null : <TabBar pending={pending} />}
    </div>
  );
}

/**
 * websockify runs beside the browser, published on the host's loopback. The
 * page is served from that same host, so its own hostname is the right one to
 * dial - hardcoding localhost would break the moment this is opened over an
 * SSH tunnel.
 */
function defaultURL(): string {
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  const port = "6080";
  return `${proto}://${window.location.hostname}:${port}/websockify`;
}
