"use client";

import { memo, useEffect, useLayoutEffect, useRef, useState, useTransition, type ReactNode } from "react";
import { PROBE_KEY } from "@/components/shell/viewport-probe";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { LogOut, WifiOff } from "lucide-react";
import { Notifications } from "@/components/shell/notifications";
import { lastIn, remember } from "@/components/shell/places";
import { ThemeToggle } from "@/components/shell/theme-toggle";
import { useRole } from "@/components/shell/role";
import { useSignOut } from "@/components/shell/sign-out";
import { UsageBars } from "@/components/shell/usage-bars";
import { focusSection, forgetFocus, onFocusRequest } from "@/lib/focus";
import { hubOf, hubsFor, pendingHome, screenOf, type Tab } from "@/lib/hubs";
import { useOnline } from "@/lib/online";
import { requestResume } from "@/lib/resume";
import type { Connection } from "@/lib/thread";
import { cn } from "@/lib/utils";

const connectionCopy: Record<Connection, { label: string; dot: string }> = {
  connecting: { label: "연결 중", dot: "bg-faint" },
  open: { label: "연결됨", dot: "bg-online" },
  closed: { label: "연결 끊김", dot: "bg-faint" },
};

/**
 * The status-bar tap every phone app answers: back to the top of what you are
 * reading. Each page reads in its own scroller (a <main>, the KakaoTalk
 * list), so the one taken is the largest on screen that is scrolled down.
 */
function scrollPageToTop() {
  let best: Element | null = null;
  let area = 0;
  const scrollers = document.querySelectorAll('[class*="overflow-y-auto"], [class*="overflow-auto"]');
  for (const el of [document.scrollingElement, ...scrollers]) {
    if (!el || el.scrollTop <= 0) continue;
    const r = el.getBoundingClientRect();
    const a = Math.max(0, Math.min(r.bottom, innerHeight) - Math.max(r.top, 0)) * r.width;
    if (a > area) {
      area = a;
      best = el;
    }
  }
  const smooth = !matchMedia("(prefers-reduced-motion: reduce)").matches;
  best?.scrollTo({ top: 0, behavior: smooth ? "smooth" : "auto" });
}

/**
 * One header for every surface, in two shapes.
 *
 * On a laptop this is a console you sit in front of, and the whole status line
 * fits on one row. On a phone it is a pager - you opened it because something
 * needs deciding - so the row keeps only the name and the connection, and the
 * tabs move to the bottom where a thumb reaches. The pending count goes with
 * them, as the same breathing dot this product already uses for a waiting
 * decision rather than a red numeric badge: red here means 반려 and 실패, and
 * spending it on "you have mail" would break the colour grammar.
 *
 * It draws no surface of its own: PageTop, below, is the glass it sits in.
 */
function HeaderView({
  connection,
  pending,
  actions,
}: {
  connection: Connection;
  pending: number;
  /** A page's own controls, first in the right-hand cluster (the chat's search). */
  actions?: ReactNode;
}) {
  const pathname = usePathname();
  const role = useRole();
  const hubs = hubsFor(role);
  const here = hubOf(hubs, pathname);
  const strip = here && here.screens.length > 1 ? here.screens : null;
  // A place of one screen says which: "Jarvis" over every one of them told
  // nobody where they were. The conversation is Jarvis's own.
  const title = here && !strip && here.screens[0].href !== "/" ? here.screens[0].label : "Jarvis";
  const router = useRouter();
  const signOut = useSignOut();
  const status = connectionCopy[connection];
  // No network at all: said in a line under the header (below), not by the
  // dot - and once it is back, every screen reads again (lib/resume.ts), as
  // the line promised.
  const online = useOnline();
  const wasOnline = useRef(online);
  useEffect(() => {
    if (online && !wasOnline.current) requestResume();
    wasOnline.current = online;
  }, [online]);
  // The stream down while the phone is online: the word stands beside the
  // dot on a phone too, where the dot alone looked like any quiet dot.
  const dropped = connection === "closed" && online;

  useEffect(() => {
    if (here) remember(here, pathname);
  }, [here, pathname]);

  // A notification's target (?focus=, lib/focus.ts): every screen has this
  // header, so every screen brings the part a notification is about into
  // view - on arrival, or when told while already showing.
  useEffect(() => {
    let stop = () => {};
    const show = (id: string) => {
      stop();
      stop = focusSection(id);
    };
    const first = new URLSearchParams(window.location.search).get("focus");
    if (first) {
      show(first);
      // After the screen's own effects have read it (they run after this
      // one: the header is their child).
      window.setTimeout(forgetFocus, 0);
    }
    const off = onFocusRequest(show);
    return () => {
      off();
      stop();
    };
  }, []);

  // Five quick taps on the name toggle the screen-measurement overlay - the
  // only way to reach it inside a home-screen app, which has no address bar.
  const taps = useRef<number[]>([]);
  function secretTap() {
    const now = Date.now();
    taps.current = [...taps.current.filter((t) => now - t < 1500), now];
    if (taps.current.length < 5) return;
    taps.current = [];
    try {
      const on = localStorage.getItem(PROBE_KEY) === "1";
      localStorage.setItem(PROBE_KEY, on ? "0" : "1");
    } catch {}
    dispatchEvent(new Event("jarvis:probe"));
  }

  return (
    <header
      className="inset-x-safe pt-safe shrink-0"
      onClick={(e) => {
        // A tap on the bar itself - not on one of its controls.
        if ((e.target as Element).closest("a, button, input, [role='button'], [role='tooltip'], [role='dialog']")) return;
        scrollPageToTop();
      }}
    >
      <div className="mx-auto flex h-13 w-full max-w-page items-center justify-between gap-2 px-4 sm:h-14 sm:px-8">
        <div className="flex min-w-0 items-baseline gap-3">
          {/* On a phone a place's screens stand where the name stood. */}
          <h1
            className={cn(
              "shrink-0 text-title font-semibold tracking-tight text-foreground select-none",
              strip && "max-sm:sr-only",
            )}
            onClick={secretTap}
          >
            {title}
          </h1>
          {strip ? <Strip screens={strip} pending={pending} className="sm:hidden" /> : null}
          {/* The same places, in the bar below, on a phone. A row that runs
              out of room scrolls inside itself; a label never breaks in two.
              Under a finger (an iPad, a phone on its side) a label is a 44px
              target; with a mouse it is only the word. */}
          <nav className="scrollbar-none hidden min-w-0 items-baseline overflow-x-auto sm:flex mouse:gap-[9px]" aria-label="화면">
            {hubs.map((hub) => {
              const active = hub === here;
              return (
                <Link
                  key={hub.id}
                  href={hub.screens[0].href}
                  onClick={(e) => {
                    e.preventDefault();
                    const href = lastIn(hub);
                    if (href !== pathname) router.push(href);
                  }}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "inline-flex h-11 shrink-0 items-center rounded-sm px-1.5 text-ui whitespace-nowrap transition-colors focus-ring mouse:h-auto mouse:px-0",
                    active ? "text-foreground" : "text-faint hover:text-dim",
                  )}
                >
                  {hub.label}
                </Link>
              );
            })}
            {strip ? (
              <>
                <span aria-hidden className="mx-2 h-3 w-px shrink-0 self-center bg-edge-soft mouse:mx-0.5" />
                <Strip screens={strip} pending={pending} wide />
              </>
            ) : null}
          </nav>
        </div>

        <div className="flex shrink-0 items-center gap-1 sm:gap-2">
          {actions}
          {/* The guest's bars are their own agent's: the subscription only,
              never what the operator's API keys cost. */}
          <UsageBars />
          {/* Dark or light, by this switch and not the phone's setting. */}
          <ThemeToggle />
          {/* Each person's own: the guest's inbox holds their jarvis's cards
              and the shared calendar, never the operator's arrivals. */}
          <Notifications />
          <span className="flex items-center gap-1.5 text-meta text-faint">
            <span
              aria-hidden
              className={cn(
                "size-[5px] rounded-full",
                status.dot,
                connection === "connecting" && "anim-breathe",
              )}
            />
            {/* The dot alone carries "연결됨" on a phone; the word is the
                luxury of a wide row - but a lost connection is said in words
                everywhere. */}
            <span className={cn(dropped ? "text-dim" : "hidden sm:inline")}>{status.label}</span>
            {dropped ? null : <span className="sr-only sm:hidden">{status.label}</span>}
          </span>
          <p className="hidden text-meta text-faint lg:block" aria-live="polite">
            {pending > 0 ? `결재 대기 ${pending}건` : "결재 대기 없음"}
          </p>
          {/* 더보기 has it on a phone: the name's row is the screens' now. */}
          <button
            type="button"
            onClick={() => void signOut()}
            aria-label="로그아웃"
            className={cn(
              "-mr-2 h-11 min-w-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring sm:mr-0 sm:px-1.5 sm:text-meta mouse:h-auto mouse:min-w-0 mouse:px-0",
              role === "guest" ? "flex" : "hidden sm:flex",
            )}
          >
            <LogOut aria-hidden className="size-4 sm:hidden" />
            <span className="hidden sm:inline">로그아웃</span>
          </button>
        </div>
      </div>
      {online ? null : <Offline />}
    </header>
  );
}

/**
 * The top of every screen - the header and what stands under it (the waiting
 * line, the conversation's search) - as one pane of frosted glass that the
 * page scrolls beneath. A page's scroller runs on under it (under-top in
 * globals.css) by its height, which is measured here as it changes - the
 * waiting line coming and going, the offline line, the search - into
 * --top-space.
 *
 * The glass has no edges: it comes out of the browser's bar above it and
 * fades out a little way below it (.glass-pane). fade={false} stops it at the
 * header, over a screen whose content starts below it and never goes under
 * it - the remote screen, the editor, and 카톡, whose rooms scroll in a box of
 * their own.
 */
export function PageTop({
  children,
  className,
  fade = true,
}: {
  children: ReactNode;
  className?: string;
  fade?: boolean;
}) {
  const pane = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = pane.current;
    if (!el) return;
    const put = () => document.documentElement.style.setProperty("--top-space", `${el.getBoundingClientRect().height}px`);
    put();
    const watch = new ResizeObserver(put);
    watch.observe(el, { box: "border-box" });
    return () => watch.disconnect();
  }, []);
  return (
    <div ref={pane} data-fade={fade ? undefined : "off"} className={cn("glass-pane relative z-30 shrink-0", className)}>
      {children}
    </div>
  );
}

/**
 * No network: one line where the standing bar would be, the same shape and
 * no colour - it is a fact about the phone, not a failure of anything asked.
 * It says why a screen could not read (the transit board's 읽지 못했어요
 * gave no reason underground) and what happens next.
 */
function Offline() {
  return (
    <div role="status" className="border-t border-edge-soft bg-glass">
      <div className="mx-auto flex w-full max-w-page items-center gap-2.5 px-4 py-2 sm:px-8">
        <WifiOff aria-hidden className="size-3.5 shrink-0 text-faint" strokeWidth={1.8} />
        <p className="min-w-0 flex-1 text-aside text-dim">인터넷 연결이 없어요 · 연결되면 다시 읽어요</p>
      </div>
    </div>
  );
}

/**
 * A place's screens, as words in the header. No second row: on a phone they
 * take the name's place, the screen you are on set the way the name was, so the
 * header says where you are and gives the page all it gave before. It moves the
 * moment one is tapped; the page follows when it is ready.
 */
function Strip({
  screens,
  pending,
  wide,
  className,
}: {
  screens: Tab[];
  pending: number;
  wide?: boolean;
  className?: string;
}) {
  const pathname = screenOf(usePathname());
  const router = useRouter();
  const [chosen, setChosen] = useState(pathname);
  const [, startNavigation] = useTransition();
  const body = (
    <>
      {screens.map((tab) => {
        const active = tab.href === chosen;
        const marked = pending > 0 && tab.href === pendingHome;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            prefetch
            draggable={false}
            aria-current={tab.href === pathname ? "page" : undefined}
            aria-label={marked ? `${tab.label}, 결재 대기 ${pending}건` : undefined}
            ref={(el) => {
              // On a narrow phone the row scrolls: the screen you are on stays in it.
              if (el && active && !wide) el.parentElement?.scrollTo({ left: el.offsetLeft - 16 });
            }}
            onClick={(e) => {
              e.preventDefault();
              setChosen(tab.href);
              if (tab.href !== pathname) startNavigation(() => router.push(tab.href));
            }}
            className={cn(
              "inline-flex h-11 shrink-0 items-center gap-1 rounded-sm px-1.5 whitespace-nowrap transition-colors focus-ring",
              wide ? "text-ui mouse:h-auto mouse:px-0" : "max-sm:first:-ml-1.5",
              wide
                ? active
                  ? "text-foreground"
                  : "text-faint hover:text-dim"
                : active
                  ? "text-title font-semibold tracking-tight text-foreground"
                  : "text-body text-faint",
            )}
          >
            {tab.label}
            {marked ? <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-foreground/80" /> : null}
          </Link>
        );
      })}
    </>
  );
  if (wide) return body;
  return (
    <nav aria-label="이곳의 화면" className={cn("scrollbar-none relative flex min-w-0 items-baseline overflow-x-auto", className)}>
      {body}
    </nav>
  );
}

// Drawn again only when what it shows changes: the thread under it renders
// on every frame of an answer.
export const Header = memo(HeaderView);
