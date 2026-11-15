"use client";

import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { usePathname } from "next/navigation";
import { Failed } from "@/components/shell/failed";
import { useRole } from "@/components/shell/role";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * PhotoCraft, the 편집 tab's image editor, kept alive between tabs.
 *
 * The editor holds its documents in the page's memory and nowhere else - the
 * web build has no autosave - so a frame that came and went with its tab
 * would throw away a picture half edited the moment someone looked at 대화.
 * It lives here instead, beside every page, from the first visit to 편집 until
 * sign-out, and stands over the space the tab leaves for it. A frame moved in
 * the DOM reloads, so it is never moved: it is placed over that space, and
 * hidden where it is when the tab is left - the same size, so the editor does
 * not lay itself out again for nothing.
 *
 * Its files are served beside the console's (scripts/photocraft.mjs): it runs
 * behind the same login, in the browser of whoever opened it, and opens and
 * saves files on that device.
 */

const SRC = "/photocraft/index.html";

/** Where the editor is drawn: the 편집 tab's empty area, while the tab is on screen. */
let slot: HTMLElement | null = null;
const listeners = new Set<() => void>();

/** The 편집 tab's ref for its empty area. */
export function editorSlot(el: HTMLElement | null) {
  if (slot === el) return;
  slot = el;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Mounted once, in the root layout. */
export function PhotoCraft() {
  // Signing out leaves the editor, and whatever was open in it, with the
  // person who used it.
  return usePathname() === "/login" ? null : <Host />;
}

type Phase = "loading" | "slow" | "ready" | "stalled" | "unsupported" | "signedOut" | "broken";

/** After this long a first visit is told why it is waiting; after the second, that it may not arrive. */
const SLOW_MS = 6_000;
const STALLED_MS = 30_000;

function Host() {
  const here = useSyncExternalStore(subscribe, () => slot, () => null);
  // Opened on the first visit to 편집, and from then on kept.
  const [opened, setOpened] = useState(false);
  if (here && !opened) setOpened(true);

  const box = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const [phase, setPhase] = useState<Phase>("loading");
  const [attempt, setAttempt] = useState(0);

  // Over the slot, following it: the waiting line above it comes and goes,
  // and a phone turns. Written to the element directly - a resize is a style
  // change, not a render.
  useLayoutEffect(() => {
    const el = box.current;
    if (!here || !el) return;
    const place = () => {
      const r = here.getBoundingClientRect();
      el.style.top = `${r.top}px`;
      el.style.left = `${r.left}px`;
      el.style.width = `${r.width}px`;
      el.style.height = `${r.height}px`;
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(here);
    addEventListener("resize", place);
    return () => {
      observer.disconnect();
      removeEventListener("resize", place);
    };
  }, [here, opened]);

  // The app removes its own "Loading PhotoCraft…" once it runs, and writes
  // "PhotoCraft failed to start" into it when the browser has neither WebGPU
  // nor WebGL2. The frame is this origin's, so that is read straight from it.
  useEffect(() => {
    if (!opened) return;
    const started = Date.now();
    const timer = setInterval(() => {
      const doc = frame.current?.contentDocument;
      if (doc === undefined) return;
      const stop = (end: Phase) => {
        clearInterval(timer);
        setPhase(end);
      };
      // Another origin: Access sent the frame to its sign-in page.
      if (doc === null) return stop("signedOut");
      const waited = Date.now() - started;
      // The frame's first, empty document, or the page still arriving.
      if (doc.URL === "about:blank" || doc.readyState === "loading") {
        if (waited > STALLED_MS) setPhase("stalled");
        return;
      }
      const loading = doc.getElementById("photocraft_loading");
      // Not the editor's page at all: the files were not deployed.
      if (!doc.getElementById("photocraft_canvas")) return stop("broken");
      if (!loading) return stop("ready");
      if (/failed to start/i.test(loading.textContent ?? "")) return stop("unsupported");
      // Still downloading. A slow line can still finish: keep looking.
      setPhase(waited > STALLED_MS ? "stalled" : waited > SLOW_MS ? "slow" : "loading");
    }, 250);
    return () => clearInterval(timer);
  }, [opened, attempt]);

  // Arriving at 편집, the keys are the editor's: its shortcuts work at once.
  // The app listens on its canvas, not on the frame's page, so the canvas is
  // what takes the focus.
  useEffect(() => {
    if (!here || phase !== "ready") return;
    const canvas = frame.current?.contentDocument?.getElementById("photocraft_canvas");
    (canvas ?? frame.current)?.focus({ preventScroll: true });
  }, [here, phase]);

  if (!opened) return null;

  return (
    <div
      ref={box}
      // Under the header's panels and sheets (z-50) and the tab bar (z-40).
      className="fixed z-30 bg-background"
      style={{ visibility: here ? "visible" : "hidden" }}
      // Away from the tab, it takes no keys and no taps.
      inert={!here}
    >
      <iframe
        key={attempt}
        ref={frame}
        src={SRC}
        title="PhotoCraft 이미지 편집기"
        allow="fullscreen; clipboard-read; clipboard-write"
        // Every gesture over the editor is the editor's: the console's own
        // (pull to refresh, the home-screen app's pinch lock) stay outside.
        className={cn(
          "block size-full touch-none border-0 transition-opacity duration-150 motion-reduce:transition-none",
          phase === "ready" ? "opacity-100" : "opacity-0",
        )}
      />
      {phase === "ready" ? null : (
        <Status
          phase={phase}
          onRetry={() => {
            setPhase("loading");
            setAttempt((n) => n + 1);
          }}
        />
      )}
    </div>
  );
}

/**
 * Why the editor did not open, what can be done about it, and - only where
 * trying again can change it - 다시 시도. Each cause on its own: two in one
 * sentence ("로그인이 만료됐거나 … 배포되지 않았어요") told no one what to do,
 * and 다시 시도 made a frame that went to the same sign-in page again. Nothing
 * here asks for what cannot be done where it is read: an iPhone's home-screen
 * app has no other browser to open it in.
 */
function Problem({ phase, onRetry }: { phase: Phase; onRetry: () => void }) {
  const guest = useRole() === "guest";
  if (phase === "stalled") {
    return (
      <Failed onRetry={onRetry}>
        <span className="block text-body text-ink">편집기 파일을 아직 받지 못했어요</span>
        연결을 확인한 뒤 다시 시도해 주세요.
      </Failed>
    );
  }
  if (phase === "unsupported") {
    // The browser has neither WebGPU nor WebGL2: the same on every try.
    return (
      <Failed>
        <span className="block text-body text-ink">이 기기에서는 편집기를 열 수 없어요</span>
        편집기에 필요한 그래픽 기능(WebGPU·WebGL2)을 이 브라우저에서 쓸 수 없어요.{" "}
        <span className="pointer-fine:hidden">컴퓨터의 브라우저에서 열어 보세요.</span>
        <span className="hidden pointer-fine:inline">최신 Chrome, Safari, Edge에서 열어 보세요.</span>
      </Failed>
    );
  }
  if (phase === "signedOut") {
    // Signing in again is the page's own, not the frame's: loaded anew, the
    // page shows Access's sign-in (as shell/session-watch.tsx does).
    return (
      <>
        <Failed>
          <span className="block text-body text-ink">로그인이 풀렸어요</span>
          다시 로그인하면 편집기가 열려요.
        </Failed>
        <Button variant="raised" size="touch-sm" onClick={() => window.location.reload()} className="mt-3">
          다시 로그인
        </Button>
      </>
    );
  }
  // A page of this origin that is not the editor: its files are not there,
  // or the server answered with an error once - which trying again can mend.
  return (
    <Failed onRetry={onRetry}>
      <span className="block text-body text-ink">편집기를 열지 못했어요</span>
      {guest
        ? "잠시 뒤 다시 시도해 주세요. 계속 안 되면 상대에게 알려 주세요."
        : "편집기 파일이 배포에서 빠진 것 같아요(scripts/photocraft.mjs). 다시 배포한 뒤 시도해 주세요."}
    </Failed>
  );
}

/** What stands in the editor's place until it runs. */
function Status({ phase, onRetry }: { phase: Phase; onRetry: () => void }) {
  const waiting = phase === "loading" || phase === "slow";
  return (
    <div className="absolute inset-0 grid place-items-center px-8">
      {waiting ? (
        <div role="status" className="text-center">
          <p className="flex items-center justify-center gap-2 text-ui text-dim">
            <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
            PhotoCraft를 여는 중
          </p>
          {phase === "slow" ? (
            <p className="mx-auto mt-1.5 max-w-[22rem] text-aside leading-relaxed text-balance text-faint">
              처음 열 때는 편집기 파일을 받느라 조금 걸려요.
            </p>
          ) : null}
        </div>
      ) : (
        <div className="w-full max-w-[24rem]">
          <Problem phase={phase} onRetry={onRetry} />
        </div>
      )}
    </div>
  );
}
