"use client";

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { type RemoteRFB, RemoteTouch } from "@/components/screen/remote-touch";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { onAsked, onShown } from "@/lib/resume";
import { SAY } from "@/lib/say";
import { HttpError } from "@/lib/use-resource";
import { useThread } from "@/lib/use-thread";
import { isPending } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * Not connected is said with why, as far as the page can tell: the ticket
 * refused (in the console server's words), no network, the screen's password
 * refused, a connection that was open and dropped, or one that never opened -
 * the container not answering, the one case the docker command is for. All of
 * them had read "브라우저 컨테이너가 응답하지 않아요" with that command.
 */
type State = { at: "connecting" | "open" } | { at: "closed"; why: string; container?: boolean };

const OPEN_FAILED = "원격 화면을 열지 못했어요.";

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
export function Screen({ vncUrl, ticketed = false }: { vncUrl: string; ticketed?: boolean }) {
  const { proposals, connection } = useThread();
  const waitingList = proposals.filter(isPending);
  const pending = waitingList.length;

  const mount = useRef<HTMLDivElement>(null);
  const rfb = useRef<RemoteRFB | null>(null);
  // A finger gets the touch layer; a mouse keeps noVNC's own handling.
  const touch = useSyncExternalStore(
    (fn) => {
      const mq = window.matchMedia("(pointer: coarse)");
      mq.addEventListener("change", fn);
      return () => mq.removeEventListener("change", fn);
    },
    () => window.matchMedia("(pointer: coarse)").matches,
    () => false,
  );
  const [state, setState] = useState<State>({ at: "connecting" });
  const [controlling, setControlling] = useState(false);
  // Each one is a connection of its own, with a ticket of its own.
  const [attempt, setAttempt] = useState(0);
  const closed = useRef(false);
  useEffect(() => {
    closed.current = state.at === "closed";
  }, [state]);

  const reconnect = useCallback(() => {
    setControlling(false);
    setState({ at: "connecting" });
    setAttempt((n) => n + 1);
  }, []);

  useEffect(() => {
    const target = mount.current;
    if (!target) return;

    let live = true;
    let client: { viewOnly: boolean; disconnect: () => void } | null = null;

    // Through the gateway the address carries a one-minute ticket, fetched
    // fresh for every connection, with the screen's own password; otherwise
    // it is fixed.
    const address: Promise<{ url: string; password?: string }> = ticketed
      ? fetch("/api/screen/ticket", { cache: "no-store" }).then(async (res) => {
          const body = await res.json().catch(() => ({}));
          if (!res.ok) throw new HttpError(res.status, body.error ?? OPEN_FAILED);
          return body as { url: string; password?: string };
        })
      : Promise.resolve({ url: vncUrl || defaultURL() });

    // noVNC touches WebSocket and the DOM on import, so it cannot be part of
    // the server bundle.
    Promise.all([import("@novnc/novnc"), address]).then(([{ default: RFB }, { url, password }]) => {
      if (!live) return;

      const instance = new RFB(target, url, password ? { credentials: { password } } : {});
      // Scale to whatever width the layout gives us; never resize the remote
      // screen, because the agent's session should not change shape when a
      // person happens to be looking at it.
      instance.scaleViewport = true;
      instance.resizeSession = false;
      instance.viewOnly = true;
      // The canvas sits in a well that already has a colour; noVNC painting
      // its own grey behind it would show as a second surface.
      instance.background = "transparent";

      let opened = false;
      let refused = false;
      instance.addEventListener("connect", () => {
        opened = true;
        if (live) setState({ at: "open" });
      });
      // A wrong password fails the handshake; a missing one leaves noVNC
      // waiting for it, which had kept "connecting" up for good.
      instance.addEventListener("securityfailure", () => {
        refused = true;
      });
      instance.addEventListener("credentialsrequired", () => {
        refused = true;
        instance.disconnect();
      });
      instance.addEventListener("disconnect", () => {
        // Over: nothing is left to disconnect, on leaving the page or the app.
        client = null;
        if (rfb.current === (instance as unknown as RemoteRFB)) rfb.current = null;
        if (!live) return;
        setControlling(false);
        setState(
          refused
            ? { at: "closed", why: "화면 비밀번호가 맞지 않아요." }
            : opened
              ? { at: "closed", why: "화면 연결이 끊겼어요." }
              : { at: "closed", why: "브라우저 컨테이너가 응답하지 않아요.", container: true },
        );
      });

      client = instance;
      rfb.current = instance as unknown as RemoteRFB;
    }).catch((err: unknown) => {
      if (!live) return;
      // A failed fetch or module load is a TypeError: the network. Anything
      // else but the ticket's own refusal speaks English, so is not repeated.
      const why = err instanceof HttpError ? err.message : err instanceof TypeError ? SAY.offline : OPEN_FAILED;
      setState({ at: "closed", why });
    });

    return () => {
      live = false;
      client?.disconnect();
      rfb.current = null;
    };
  }, [vncUrl, ticketed, attempt]);

  // A phone kills a put-away app's sockets without a word, which left the
  // last frame standing as if it were live. So the socket is closed on
  // leaving and a new one, on a new ticket, opened on coming back - once,
  // though a page back from the back-forward cache says so twice. Asked to
  // read again (the network back, a pull to refresh), a screen that is not
  // there tries once more; one that is stays as it is.
  useEffect(() => {
    let away = false;
    const hidden = () => {
      if (document.visibilityState !== "hidden") return;
      away = true;
      rfb.current?.disconnect();
    };
    document.addEventListener("visibilitychange", hidden);
    const offShown = onShown(() => {
      if (!away) return;
      away = false;
      reconnect();
    });
    const offAsked = onAsked(() => {
      if (closed.current && !away) reconnect();
    });
    return () => {
      document.removeEventListener("visibilitychange", hidden);
      offShown();
      offAsked();
    };
  }, [reconnect]);

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
      <PageTop
        fade={false}
        className={cn("transition-opacity duration-200 motion-reduce:transition-none", controlling && "opacity-25")}
      >
        <Header connection={connection} pending={pending} />
        {/* Inside the dimmed block on purpose: while you are driving the
            remote browser the room goes quiet, and that includes this. */}
        <StandingBar pending={waitingList} />
      </PageTop>

      <main
        className={cn(
          "inset-x-safe min-h-0 flex-1",
          controlling
            ? "pb-safe screen-h fixed inset-x-0 top-0 z-50 flex flex-col bg-background p-3 pt-[max(env(safe-area-inset-top),0.75rem)] sm:p-6"
            : "mx-auto flex w-full max-w-page flex-col px-4 pt-3 pb-[calc(var(--tabbar-space)+0.75rem)] sm:px-8 sm:py-4",
        )}
      >
        <div className="mb-2.5 flex shrink-0 items-center justify-between gap-3">
          {/* The well says why the screen is not in it; saying it twice here
              would be the same sentence in two places.

              Esc is the desktop way out. A phone has no Esc, so the button on
              the right is the only exit there - which is why it is named for
              leaving rather than toggled silently. */}
          <p className="min-w-0 truncate text-meta text-faint" aria-live="polite">
            {controlling ? (
              <>
                조작 중<span className="hidden sm:inline"> · Esc로 돌아가요</span>
              </>
            ) : (
              ""
            )}
          </p>
          <button
            type="button"
            onClick={toggleControl}
            disabled={state.at !== "open"}
            aria-pressed={controlling}
            className={cn(
              "flex shrink-0 items-center justify-center rounded-lg px-3 text-aside transition-colors outline-none",
              "min-h-11 mouse:min-h-0 mouse:px-2 mouse:py-1 mouse:text-aside",
              "focus-ring",
              "disabled:pointer-events-none disabled:text-faint/40",
              controlling
                ? "bg-glass-raised text-foreground"
                : "text-faint hover:text-dim",
            )}
          >
            {controlling ? "보기로 돌아가기" : "조작"}
          </button>
        </div>

        {/* No border: in this product a border is for what a hand is on - a
            decision, a field - and this is a window, not a card. It is
            recessed instead.

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
          <div ref={mount} data-no-pull className="absolute inset-0" />
          {controlling && touch && state.at === "open" ? <RemoteTouch rfb={rfb} canvasHost={mount} /> : null}

          {/* The well is the screen's place while it connects; a failure says
              why in it, with the way back. */}
          {state.at === "connecting" ? (
            <p role="status" className="absolute inset-0 flex items-center justify-center px-8 text-aside text-faint">
              화면에 연결하는 중…
            </p>
          ) : state.at === "closed" ? (
            <div className="absolute inset-0 flex items-center justify-center px-8">
              <Failed onRetry={reconnect}>
                {state.why}
                {state.container ? (
                  <code className="mt-1 block font-mono text-aside text-faint">docker compose up -d browser</code>
                ) : null}
              </Failed>
            </div>
          ) : null}
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
