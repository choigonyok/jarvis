"use client";

import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Phone } from "lucide-react";
import { Failed } from "@/components/shell/failed";
import { useRole } from "@/components/shell/role";
import { themeNow } from "@/lib/theme";
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
 *
 * 둥지 wears the console's colours and type (dungji/src/styles.css) and its
 * theme: the one the header's switch chose goes with the ticket, and a switch
 * afterwards is told to the frame (dungji/src/lib/theme.ts).
 *
 * The frame tells this page two things (dungji/src/lib/embed.ts): that its
 * home is drawn - the waiting line stays until then, so no empty page shows
 * between - and its call's state. A call that rings, or goes on, while another
 * tab is looked at would be in a hidden frame: a line at the top says so on
 * every tab, and takes one back to 둥지.
 *
 * This page tells the frame whether it is in sight. Hidden here, 둥지 still
 * looks open to itself, and what the other one does there meanwhile has to
 * reach this person as a notification rather than as a line in a frame nobody
 * sees (dungji/worker/live.ts).
 */

type CallState = { state: "idle" | "incoming" | "outgoing" | "connecting" | "active"; name: string };


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

/** Run from the home screen (the same test as components/shell/no-zoom.tsx). */
function standalone() {
  return matchMedia("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

/** An older 둥지 that does not say it is drawn: shown this long after its page has loaded. */
const READY_FALLBACK_MS = 6_000;

/** A page that has not arrived by now is not coming on this try. */
const STALLED_MS = 25_000;

function Host() {
  const here = useSyncExternalStore(subscribe, () => slot, () => null);
  // Opened on the first visit to 둥지, and from then on kept.
  const [opened, setOpened] = useState(false);
  if (here && !opened) setOpened(true);

  const box = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const [src, setSrc] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>("ticket");
  const [attempt, setAttempt] = useState(0);
  const [call, setCall] = useState<CallState>({ state: "idle", name: "" });
  const router = useRouter();
  const shown = here !== null;
  const shownNow = useRef(shown);

  // What the frame says: that it is drawn, and its call. Only from 둥지's own origin.
  // A frame that has just drawn is told at once whether it is in sight.
  useEffect(() => {
    if (!src) return;
    const origin = new URL(src).origin;
    const hear = (e: MessageEvent) => {
      if (e.origin !== origin) return;
      const data = e.data as { type?: string; state?: CallState["state"]; name?: string } | null;
      if (data?.type === "dungji:ready") {
        setPhase("ready");
        frame.current?.contentWindow?.postMessage({ type: "dungji:shown", shown: shownNow.current }, origin);
      }
      if (data?.type === "dungji:call" && data.state) setCall({ state: data.state, name: data.name ?? "" });
    };
    addEventListener("message", hear);
    return () => removeEventListener("message", hear);
  }, [src]);

  // In sight or not, as the tab comes and goes.
  useEffect(() => {
    shownNow.current = shown;
    if (src) frame.current?.contentWindow?.postMessage({ type: "dungji:shown", shown }, new URL(src).origin);
  }, [src, shown]);

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
        // From the home screen, zoom is locked (components/shell/no-zoom.tsx), and a
        // pinch inside the frame never reaches that lock: 둥지 locks its own.
        setSrc(`${url}&theme=${themeNow()}${standalone() ? "&app=1" : ""}`);
        setPhase("loading");
      })
      .catch(() => !gone && setPhase("failed"));
    return () => {
      gone = true;
    };
  }, [opened, attempt]);

  // The header's switch, while 둥지 is open: tell the frame, which repaints itself.
  useEffect(() => {
    if (!src) return;
    const origin = new URL(src).origin;
    const tell = () => frame.current?.contentWindow?.postMessage({ type: "dungji:theme", theme: themeNow() }, origin);
    const watch = new MutationObserver(tell);
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => watch.disconnect();
  }, [src]);

  // A phone's keyboard comes up over this page, and the frame keeps its size:
  // 둥지 is told how much of its bottom the keyboard covers, so a sheet in it
  // can lift its save row above the keys (dungji/src/ui/sheet.tsx).
  useEffect(() => {
    const vv = window.visualViewport;
    const el = box.current;
    if (!src || !vv || !el) return;
    const origin = new URL(src).origin;
    let told = -1;
    const tell = () => {
      // Zoomed in, the visible area is small for another reason.
      const covered = Math.abs(vv.scale - 1) > 0.01 ? 0 : Math.max(0, Math.round(el.getBoundingClientRect().bottom - (vv.offsetTop + vv.height)));
      if (covered === told) return;
      told = covered;
      frame.current?.contentWindow?.postMessage({ type: "dungji:keyboard", bottom: covered }, origin);
    };
    vv.addEventListener("resize", tell);
    vv.addEventListener("scroll", tell);
    return () => {
      vv.removeEventListener("resize", tell);
      vv.removeEventListener("scroll", tell);
    };
  }, [src, opened]);

  useEffect(() => {
    if (phase !== "loading") return;
    const timer = setTimeout(() => setPhase("failed"), STALLED_MS);
    return () => clearTimeout(timer);
  }, [phase, attempt]);

  if (!opened) return null;

  const calling = call.state !== "idle" && !here;

  return (
    <>
      {calling ? (
        <button
          type="button"
          onClick={() => router.push("/nest")}
          // Above the header's glass and the tab bar, on whichever tab is open.
          className="liquid-glass fixed left-1/2 z-50 flex h-11 -translate-x-1/2 items-center gap-2 rounded-full pr-4 pl-3.5 text-ui text-foreground focus-ring"
          style={{ top: "calc(var(--top-space) + 0.5rem)" }}
        >
          <Phone aria-hidden className={cn("size-4", call.state === "incoming" && "anim-breathe")} strokeWidth={1.8} />
          {call.state === "incoming"
            ? `${call.name}님의 전화 · 받으러 가기`
            : call.state === "active"
              ? `${call.name}님과 통화 중 · 둥지로`
              : `${call.name}님과 잇는 중 · 둥지로`}
        </button>
      ) : null}
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
            ref={frame}
            src={src}
            title="둥지"
            allow="microphone; autoplay; clipboard-write; web-share"
            onLoad={() => setTimeout(() => setPhase((p) => (p === "loading" ? "ready" : p)), READY_FALLBACK_MS)}
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
    </>
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
