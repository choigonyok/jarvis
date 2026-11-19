"use client";

import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { usePathname } from "next/navigation";
import { Failed } from "@/components/shell/failed";
import { useRole } from "@/components/shell/role";
import { cn } from "@/lib/utils";

/**
 * 둥지 (dungji.choigonyok.com), framed and kept alive between tabs.
 *
 * A voice call in 둥지 runs in the frame's page: a frame that came and went
 * with its tab would hang up the moment someone looked at 대화. It lives here
 * instead, beside every page, from the first visit to 둥지 until sign-out,
 * and stands over the space the tab leaves for it (components/nest/nest.tsx);
 * away from the tab it is hidden where it is. A frame moved in the DOM
 * reloads, so it is never moved.
 *
 * It opens with a one-minute ticket from /api/nest/ticket: 둥지 is another
 * site with its own accounts, and the ticket is how it knows who is here.
 * Both are under choigonyok.com, so 둥지's own sign-in cookie, set inside this
 * frame, is a first-party one - Safari keeps it. The frame may use the
 * microphone (the call) and nothing else of this page.
 */

let slot: HTMLElement | null = null;
const listeners = new Set<() => void>();

/** The 둥지 tab's ref for its empty area. */
export function nestSlot(el: HTMLElement | null) {
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
export function NestHost() {
  // Signing out leaves 둥지, and a call in it, with the person who used it.
  return usePathname() === "/login" ? null : <Host />;
}

type Phase = "ticket" | "loading" | "ready" | "unset" | "failed";

/** A page that has not arrived by now is not coming on this try. */
const STALLED_MS = 25_000;

function Host() {
  const here = useSyncExternalStore(subscribe, () => slot, () => null);
  // Opened on the first visit to 둥지, and from then on kept.
  const [opened, setOpened] = useState(false);
  if (here && !opened) setOpened(true);

  const box = useRef<HTMLDivElement>(null);
  const [src, setSrc] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>("ticket");
  const [attempt, setAttempt] = useState(0);

  // Over the slot, following it - written to the element directly: a resize
  // is a style change, not a render.
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

  // Each try asks for a fresh ticket: a ticket is taken once, and is good for a minute.
  useEffect(() => {
    if (!opened) return;
    let gone = false;
    fetch("/api/nest/ticket", { cache: "no-store" })
      .then(async (r) => {
        if (gone) return;
        if (r.status === 503) return setPhase("unset");
        if (!r.ok) return setPhase("failed");
        const { url } = (await r.json()) as { url: string };
        if (gone) return;
        setSrc(url);
        setPhase("loading");
      })
      .catch(() => !gone && setPhase("failed"));
    return () => {
      gone = true;
    };
  }, [opened, attempt]);

  useEffect(() => {
    if (phase !== "loading") return;
    const timer = setTimeout(() => setPhase("failed"), STALLED_MS);
    return () => clearTimeout(timer);
  }, [phase, attempt]);

  if (!opened) return null;

  return (
    <div
      ref={box}
      // Under the header's panels and sheets (z-50) and the tab bar (z-40).
      className="fixed z-30 bg-background"
      style={{ visibility: here ? "visible" : "hidden" }}
      // Away from the tab, it takes no keys and no taps (a call in it goes on).
      inert={!here}
    >
      {src ? (
        <iframe
          key={attempt}
          src={src}
          title="둥지"
          allow="microphone; autoplay; clipboard-write; web-share"
          onLoad={() => setPhase("ready")}
          className={cn(
            "block size-full border-0 transition-opacity duration-150 motion-reduce:transition-none",
            phase === "ready" ? "opacity-100" : "opacity-0",
          )}
        />
      ) : null}
      {phase === "ready" ? null : (
        <Status
          phase={phase}
          onRetry={() => {
            setPhase("ticket");
            setSrc(null);
            setAttempt((n) => n + 1);
          }}
        />
      )}
    </div>
  );
}

/** What stands in 둥지's place until it opens, or why it did not. */
function Status({ phase, onRetry }: { phase: Phase; onRetry: () => void }) {
  const guest = useRole() === "guest";
  if (phase === "ticket" || phase === "loading")
    return (
      <div className="absolute inset-0 grid place-items-center px-8">
        <p role="status" className="flex items-center justify-center gap-2 text-ui text-dim">
          <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
          둥지를 여는 중
        </p>
      </div>
    );
  return (
    <div className="absolute inset-0 grid place-items-center px-8">
      <div className="w-full max-w-[24rem]">
        {phase === "unset" ? (
          <Failed>
            <span className="block text-body text-ink">둥지가 아직 연결되지 않았어요</span>
            {guest
              ? "상대가 둥지를 연결하면 여기서 열려요."
              : "둥지 Worker 를 배포하고 DUNGJI_SSO_SECRET 을 두 Worker 에 넣어야 해요(DEPLOY.md '둥지')."}
          </Failed>
        ) : (
          <Failed onRetry={onRetry}>
            <span className="block text-body text-ink">둥지를 열지 못했어요</span>
            연결을 확인한 뒤 다시 시도해 주세요.
          </Failed>
        )}
      </div>
    </div>
  );
}
