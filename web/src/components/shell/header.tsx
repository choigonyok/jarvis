"use client";

import { memo, useEffect, useRef, useState, useTransition, type ReactNode } from "react";
import { PROBE_KEY } from "@/components/shell/viewport-probe";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  Activity,
  BookUser,
  Brush,
  CalendarDays,
  ClipboardCheck,
  Dumbbell,
  Ellipsis,
  LayoutGrid,
  LogOut,
  type LucideIcon,
  MapPin,
  MessageCircle,
  MessagesSquare,
  MonitorPlay,
  ReceiptText,
  ListChecks,
  Images,
  Sun,
  Target,
  TrainFront,
  Wallet,
  WifiOff,
} from "lucide-react";
import { BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Sheet, SheetTrigger } from "@/components/ui/sheet";
import { Notifications } from "@/components/shell/notifications";
import { useRole } from "@/components/shell/role";
import { UsageBars } from "@/components/shell/usage-bars";
import { focusSection, forgetFocus, onFocusRequest } from "@/lib/focus";
import { useOnline } from "@/lib/online";
import { requestResume } from "@/lib/resume";
import type { Role } from "@/lib/role";
import { dropPush } from "@/lib/use-push";
import type { Connection } from "@/lib/thread";
import { cn } from "@/lib/utils";

const connectionCopy: Record<Connection, { label: string; dot: string }> = {
  connecting: { label: "연결 중", dot: "bg-faint" },
  open: { label: "연결됨", dot: "bg-online" },
  closed: { label: "연결 끊김", dot: "bg-faint" },
};

type Tab = { href: string; label: string };

/** Line icons, the system's own weight: on the glass bar a label alone reads as a button row. */
const ICON: Record<string, LucideIcon> = {
  "/": MessageCircle,
  "/workout": Dumbbell,
  "/record": ClipboardCheck,
  "/assets": Wallet,
  "/spending": ReceiptText,
  "/jobs": ListChecks,
  "/goals": Target,
  "/photos": Images,
  "/edit": Brush,
  "/life": BookUser,
  "/location": MapPin,
  "/transit": TrainFront,
  "/calendar": CalendarDays,
  "/screen": MonitorPlay,
  "/kakao": MessagesSquare,
  "/status": Activity,
};

/** One place on the phone's bar, and the screens that live there. */
type Hub = {
  id: string;
  label: string;
  icon: LucideIcon;
  screens: Tab[];
  /** Opens as a list of its screens: none of them is where you would land. */
  list?: boolean;
};

/**
 * Sixteen screens are more than a thumb bar can hold - five is the limit before
 * a slot is too narrow for its word. So the bar names five places, by what you
 * came for, and a place's screens sit in the header where the name was: going
 * between them costs no row, and each stays one short page. 더보기 is the screens
 * you reach for only when something is up.
 */
const OWNER_HUBS: Hub[] = [
  { id: "talk", label: "대화", icon: MessageCircle, screens: [{ href: "/", label: "대화" }] },
  {
    id: "today",
    label: "오늘",
    icon: Sun,
    screens: [
      { href: "/calendar", label: "일정" },
      { href: "/jobs", label: "작업" },
      { href: "/goals", label: "목표" },
      { href: "/workout", label: "운동" },
      { href: "/transit", label: "출발" },
      { href: "/record", label: "기록" },
    ],
  },
  {
    id: "money",
    label: "돈",
    icon: Wallet,
    screens: [
      { href: "/assets", label: "자산" },
      { href: "/spending", label: "가계부" },
    ],
  },
  {
    id: "life",
    label: "생활",
    icon: BookUser,
    screens: [
      { href: "/life", label: "나" },
      { href: "/photos", label: "사진" },
      { href: "/location", label: "위치" },
    ],
  },
  {
    id: "more",
    label: "더보기",
    icon: Ellipsis,
    list: true,
    screens: [
      { href: "/kakao", label: "카톡" },
      { href: "/screen", label: "화면" },
      { href: "/status", label: "상태" },
    ],
  },
];

/**
 * The guest's places, grouped as the operator's are: eight screens were eight
 * wordless icons, more slots than the operator's five, and every one a single
 * screen, so neither the bar nor the header said where you were (2026-11-06
 * review). Five places keep their words.
 */
const GUEST_HUBS: Hub[] = [
  { id: "talk", label: "대화", icon: MessageCircle, screens: [{ href: "/", label: "대화" }] },
  {
    id: "today",
    label: "오늘",
    icon: Sun,
    screens: [
      { href: "/calendar", label: "캘린더" },
      { href: "/jobs", label: "작업" },
      { href: "/goals", label: "목표" },
      { href: "/transit", label: "출발" },
    ],
  },
  { id: "workout", label: "운동", icon: Dumbbell, screens: [{ href: "/workout", label: "운동" }] },
  { id: "location", label: "위치", icon: MapPin, screens: [{ href: "/location", label: "위치" }] },
  { id: "edit", label: "편집", icon: Brush, screens: [{ href: "/edit", label: "편집" }] },
];

/** The places a role is shown. The middleware is what actually refuses the rest. */
const hubsFor = (role: Role) => (role === "guest" ? GUEST_HUBS : OWNER_HUBS);

/** The place a screen belongs to. 편집 is reached from 사진 and is no screen of its own there. */
const hubOf = (hubs: Hub[], pathname: string) =>
  hubs.find((h) => h.screens.some((s) => s.href === pathname)) ?? (pathname === "/edit" ? hubs.find((h) => h.id === "life") : undefined);

/** How long a finger rests on a place before its screens come up. */
const HOLD_MS = 350;
const MENU_WIDTH = 148;

const LAST = "jarvis:hub:";

/** A place opens on the screen left open there, on this device. */
function lastIn(hub: Hub): string {
  try {
    const saved = localStorage.getItem(LAST + hub.id);
    if (saved && hub.screens.some((s) => s.href === saved)) return saved;
  } catch {}
  return hub.screens[0].href;
}

function remember(hub: Hub, pathname: string) {
  if (hub.screens.length < 2 || !hub.screens.some((s) => s.href === pathname)) return;
  try {
    localStorage.setItem(LAST + hub.id, pathname);
  } catch {}
}

/**
 * Where a waiting decision is announced: the conversation, where the card
 * stands and is decided - the mark sat on 더보기, two taps from a card that
 * was a tab away (2026-11-06 review).
 */
const pendingHome = "/";

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
 * The header stays solid, with plain text tabs on a wide screen. Only the
 * phone's floating tab bar is glass: two backdrop-filter bars on one screen
 * sample each other in Chrome, which once showed as the tab labels ghosting
 * into the header.
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
  // dot on a phone too, where the dot alone looked like any quiet dot
  // (2026-11-07 review).
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
      className="inset-x-safe pt-safe shrink-0 border-b border-edge-soft bg-background"
      onClick={(e) => {
        // A tap on the bar itself - not on one of its controls.
        if ((e.target as Element).closest("a, button, input, [role='button'], [role='tooltip'], [role='dialog']")) return;
        scrollPageToTop();
      }}
    >
      <div className="mx-auto flex h-13 w-full max-w-[56rem] items-center justify-between gap-2 px-4 sm:h-14 sm:px-8">
        <div className="flex min-w-0 items-baseline gap-3">
          {/* On a phone a place's screens stand where the name stood. */}
          <h1
            className={cn(
              "shrink-0 text-[15px] font-semibold tracking-tight text-foreground select-none",
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
                    "inline-flex h-11 shrink-0 items-center rounded-sm px-1.5 text-[13px] whitespace-nowrap transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-auto mouse:px-0",
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
          {/* Each person's own: the guest's inbox holds their jarvis's cards
              and the shared calendar, never the operator's arrivals. */}
          <Notifications />
          <span className="flex items-center gap-1.5 text-[11.5px] text-faint">
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
          <p className="hidden text-[11.5px] text-faint lg:block" aria-live="polite">
            {pending > 0 ? `결재 대기 ${pending}건` : "결재 대기 없음"}
          </p>
          {/* 더보기 has it on a phone: the name's row is the screens' now. */}
          <button
            type="button"
            onClick={() => void signOut()}
            aria-label="로그아웃"
            className={cn(
              "-mr-2 h-11 min-w-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:mr-0 sm:px-1.5 sm:text-[11.5px] mouse:h-auto mouse:min-w-0 mouse:px-0",
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
 * No network: one line where the standing bar would be, the same shape and
 * no colour - it is a fact about the phone, not a failure of anything asked.
 * It says why a screen could not read (the transit board's 읽지 못했어요
 * gave no reason underground) and what happens next.
 */
function Offline() {
  return (
    <div role="status" className="border-t border-edge-soft bg-glass">
      <div className="mx-auto flex w-full max-w-[46rem] items-center gap-2.5 px-4 py-2 sm:px-8">
        <WifiOff aria-hidden className="size-3.5 shrink-0 text-faint" strokeWidth={1.8} />
        <p className="min-w-0 flex-1 text-[12.5px] text-dim">인터넷 연결이 없어요 · 연결되면 다시 읽어요</p>
      </div>
    </div>
  );
}

function useSignOut() {
  const router = useRouter();
  return async () => {
    // 세션이 남아 있을 때 지운다 - 로그아웃한 뒤에는 notify 에 닿지 못한다.
    await dropPush();
    const res = await fetch("/api/auth/logout", { method: "POST" }).catch(() => null);
    const body = (await res?.json().catch(() => null)) as { next?: string } | null;
    if (body?.next && body.next !== "/login") {
      // Cloudflare Access's own sign-out: a full page load, not a client route.
      window.location.href = body.next;
      return;
    }
    router.replace("/login");
    router.refresh();
  };
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
  const pathname = usePathname();
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
              "inline-flex h-11 shrink-0 items-center gap-1 rounded-sm px-1.5 whitespace-nowrap transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              wide ? "text-[13px] mouse:h-auto mouse:px-0" : "max-sm:first:-ml-1.5",
              wide
                ? active
                  ? "text-foreground"
                  : "text-faint hover:text-dim"
                : active
                  ? "text-[15px] font-semibold tracking-tight text-foreground"
                  : "text-[14px] text-faint",
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

/**
 * The phone's navigation: a capsule of glass floating over the page, lifted
 * off the edges and the home indicator, the way iOS 26 draws its tab bar. The
 * page scrolls beneath it (see --tabbar-space in globals.css, which gives the
 * scrollers room at the end). A lens of brighter glass sits under the current
 * tab and slides when you tap another. Pressed and held, a place lists its
 * screens above the bar, to slide up to and let go on.
 *
 * It hides while something is being typed - the keyboard has covered it
 * anyway, and the space it gives back is what the transcript needs most then.
 */
function TabBarView({
  pending,
  hidden,
}: {
  pending: number;
  hidden?: boolean;
}) {
  const pathname = usePathname();
  const router = useRouter();
  const role = useRole();
  const [more, setMore] = useState(false);
  const [navigating, startNavigation] = useTransition();
  const signOut = useSignOut();

  const hubs = hubsFor(role);
  // Five places have room for their word.
  const worded = hubs.length <= 5;
  const here = hubOf(hubs, pathname);
  const at = here ? hubs.indexOf(here) : -1;
  const slots = hubs.length;
  const moreSlot = hubs.findIndex((h) => h.list);
  const inMore = at >= 0 && at === moreSlot;
  const moreMarked = pending > 0 && moreSlot >= 0 && hubs[moreSlot].screens.some((s) => s.href === pendingHome);

  // Where the lens is. It moves the moment a tab is chosen, not when the page
  // has loaded: the bar answers the finger, and the page follows when it is
  // ready (the old one stays up until then - a transition, not a blank).
  const [chosen, setChosen] = useState(at);
  useEffect(() => {
    // The route settled (or changed from elsewhere): the lens follows it.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setChosen(at);
  }, [at]);

  // Every screen is prefetched, so most switches have nothing left to wait for.
  useEffect(() => {
    for (const hub of hubs) for (const tab of hub.screens) router.prefetch(tab.href);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [role]);

  function go(slot: number) {
    if (slot === moreSlot && !inMore) {
      setMore(true);
      return;
    }
    const hub = hubs[slot];
    if (!hub) return;
    setChosen(slot);
    // A place opens where it was left; its own slot again goes round its screens.
    const next = (hub.screens.findIndex((s) => s.href === pathname) + 1) % hub.screens.length;
    const href = slot === at ? hub.screens[next].href : lastIn(hub);
    if (href !== pathname) startNavigation(() => router.push(href));
  }

  // Dragging along the bar, as on iOS: the lens follows the finger and the tab
  // under it on release is the one opened.
  const track = useRef<HTMLDivElement>(null);
  const drag = useRef<{
    startX: number;
    moved: boolean;
    width: number;
    left: number;
    slot: number;
    /** The finger is choosing among a place's screens. */
    menu: boolean;
    /** It has been over one of them. */
    entered: boolean;
  } | null>(null);
  // Where the finger is along the bar, and the slot under it - both worked out
  // in the pointer handler, which is where the bar's width is known.
  const [held, setHeld] = useState<{ x: number; slot: number } | null>(null);
  const suppressClick = useRef(false);
  const slotAt = (x: number, width: number) =>
    Math.min(slots - 1, Math.max(0, Math.floor((x / width) * slots)));

  // A finger resting on a place brings up its screens: it slides up to one and
  // lets go, one gesture. Let go at once, the list stays for a tap.
  const [menu, setMenu] = useState<{ slot: number; at: number; left: number; pinned: boolean } | null>(null);
  const menuBox = useRef<HTMLDivElement>(null);
  const holding = useRef<number | undefined>(undefined);
  const stopHold = () => window.clearTimeout(holding.current);

  function startHold(slot: number, el: HTMLElement, pointer: number) {
    stopHold();
    if (hubs[slot].screens.length < 2) return;
    holding.current = window.setTimeout(() => {
      const d = drag.current;
      if (!d) return;
      d.menu = true;
      el.setPointerCapture(pointer);
      setHeld(null);
      const center = d.left + ((slot + 0.5) * d.width) / slots;
      setMenu({ slot, at: -1, left: Math.max(12, Math.min(center - MENU_WIDTH / 2, innerWidth - MENU_WIDTH - 12)), pinned: false });
      navigator.vibrate?.(8);
    }, HOLD_MS);
  }

  /** The screen under the finger: the row it is level with, wherever it is across. */
  function itemAt(y: number, slot: number) {
    const box = menuBox.current?.getBoundingClientRect();
    if (!box || y < box.top - 24 || y > box.bottom + 6) return -1;
    const n = hubs[slot].screens.length;
    return Math.min(n - 1, Math.max(0, Math.floor(((y - box.top) / box.height) * n)));
  }

  function openScreen(slot: number, href: string) {
    setMenu(null);
    setChosen(slot);
    if (href !== pathname) startNavigation(() => router.push(href));
  }

  function onPointerDown(e: React.PointerEvent<HTMLDivElement>) {
    const box = track.current?.getBoundingClientRect();
    if (!box) return;
    setMenu(null);
    const slot = slotAt(e.clientX - box.left, box.width);
    drag.current = { startX: e.clientX, moved: false, width: box.width, left: box.left, slot, menu: false, entered: false };
    startHold(slot, e.currentTarget, e.pointerId);
  }
  function onPointerMove(e: React.PointerEvent<HTMLDivElement>) {
    const d = drag.current;
    if (!d) return;
    if (d.menu) {
      const at = itemAt(e.clientY, d.slot);
      if (at >= 0) d.entered = true;
      setMenu((m) => (m && m.at !== at ? { ...m, at } : m));
      return;
    }
    if (!d.moved && Math.abs(e.clientX - d.startX) < 8) return;
    if (!d.moved) {
      d.moved = true;
      // From here the bar owns the gesture: the page does not scroll, and the
      // tab the press started on is not "clicked" on release.
      e.currentTarget.setPointerCapture(e.pointerId);
    }
    const x = Math.min(d.width, Math.max(0, e.clientX - d.left));
    const slot = slotAt(x, d.width);
    // Sliding along the bar and resting on a place opens it the same way.
    if (slot !== d.slot) {
      d.slot = slot;
      startHold(slot, e.currentTarget, e.pointerId);
    }
    setHeld({ x, slot });
  }
  function onPointerUp(e: React.PointerEvent<HTMLDivElement>) {
    stopHold();
    const d = drag.current;
    drag.current = null;
    if (!d?.menu && !d?.moved) return;
    suppressClick.current = true;
    setTimeout(() => (suppressClick.current = false), 0);
    setHeld(null);
    if (d.menu) {
      const at = itemAt(e.clientY, d.slot);
      if (at >= 0) openScreen(d.slot, hubs[d.slot].screens[at].href);
      // Slid over the list and away: called off.
      else if (d.entered) setMenu(null);
      else setMenu((m) => m && { ...m, at: -1, pinned: true });
      return;
    }
    go(slotAt(Math.min(d.width, Math.max(0, e.clientX - d.left)), d.width));
  }
  function onPointerCancel() {
    stopHold();
    drag.current = null;
    setHeld(null);
    setMenu(null);
  }

  const dragging = held !== null;
  const lit = held ? held.slot : menu ? menu.slot : chosen;
  const slotWidth = 100 / slots;

  // A slot is an equal share of the bar rather than a fixed 44px: the bar's
  // width (below) is what keeps the targets, and on a narrow phone each share
  // gives a little instead of pushing the last one off the screen.
  const item =
    "liquid-press relative z-10 flex min-h-11 min-w-0 flex-1 flex-col items-center justify-center gap-[3px] rounded-full outline-none focus-visible:ring-3 focus-visible:ring-ring/50";
  const icon = worded ? "size-5" : "size-[22px]";
  const word = (label: string) => (worded ? <span className="text-[10.5px] leading-none">{label}</span> : null);

  return (
    <nav
      aria-label="화면"
      data-tabbar
      data-hidden={hidden || undefined}
      className={cn(
        "screen-h pointer-events-none fixed inset-x-0 top-0 z-40 flex flex-col justify-end transition-[transform,opacity] duration-300 ease-out motion-reduce:transition-none sm:hidden",
        hidden ? "translate-y-[140%] opacity-0" : "translate-y-0 opacity-100",
      )}
      // Off the edges like the system bar: a gutter on each side, never inside
      // the rounded corners or under the home indicator.
      style={{
        paddingBottom: "max(env(safe-area-inset-bottom), 0.5rem)",
        paddingLeft: "max(env(safe-area-inset-left), 1rem)",
        paddingRight: "max(env(safe-area-inset-right), 1rem)",
      }}
    >
      {menu?.pinned ? (
        <button
          type="button"
          aria-label="닫기"
          onClick={() => setMenu(null)}
          className="pointer-events-auto absolute inset-0 outline-none"
        />
      ) : null}
      <div
        className="liquid-glass pointer-events-auto relative mx-auto flex h-[3.75rem] w-full rounded-full p-1.5"
        // 60px a slot with its word, 44px an icon alone, and no wider - a short
        // bar for a short list does not stretch edge to edge - and never wider
        // than the space between the gutters. Eight tabs at a fixed 44px were
        // 364px, more than a 390px phone has (358): the bar ran into the right
        // gutter, and on a 360px phone cut 더보기 off.
        style={{ maxWidth: `calc(${slots} * ${worded ? "3.75rem" : "2.75rem"} + 0.75rem)` }}
      >
        <div
          ref={track}
          className="relative flex flex-1 touch-none select-none"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerCancel}
          // Android's own long press on a link.
          onContextMenu={(e) => e.preventDefault()}
        >
          {/* The lens: one slot wide. It sits on the chosen tab, follows the
              finger while dragging, and breathes while the page loads. */}
          <span
            aria-hidden
            className={cn(
              "liquid-lens pointer-events-none absolute inset-y-0 left-0 rounded-full",
              dragging && "liquid-lens-held",
              navigating && !dragging && "liquid-lens-waiting",
            )}
            style={{
              width: `${slotWidth}%`,
              transform: dragging
                ? // Scaled after the move, so the growth does not stretch the
                  // distance too and drift the lens off the finger.
                  `translateX(calc(${held.x}px - 50%)) scale(1.12)`
                : `translateX(${Math.max(lit, 0) * 100}%)`,
              opacity: lit >= 0 || dragging ? 1 : 0,
            }}
          />
          {hubs.map((hub, slot) => {
            if (hub.list) return null;
            const active = slot === lit;
            const Icon = hub.icon;
            // Only the surface that renders the queue wears the mark, so it
            // reads as "there is something to decide over there".
            const marked = pending > 0 && hub.screens.some((s) => s.href === pendingHome);
            return (
              <Link
                key={hub.id}
                href={hub.screens[0].href}
                prefetch
                draggable={false}
                aria-label={marked ? `${hub.label}, 결재 대기 ${pending}건` : hub.label}
                title={hub.label}
                aria-current={here === hub ? "page" : undefined}
                onClick={(e) => {
                  // Navigation is ours, so the lens can move before the page does.
                  e.preventDefault();
                  if (suppressClick.current) return;
                  go(slot);
                }}
                className={cn(item, active ? "text-foreground" : "text-dim")}
              >
                <span className="relative">
                  <Icon aria-hidden className={icon} strokeWidth={active ? 2.1 : 1.7} />
                  {marked ? (
                    <span
                      aria-hidden
                      className="anim-breathe absolute -top-0.5 -right-1.5 size-[6px] rounded-full bg-foreground/80"
                    />
                  ) : null}
                </span>
                {word(hub.label)}
              </Link>
            );
          })}

          {moreSlot >= 0 ? (
            <Sheet open={more} onOpenChange={setMore}>
              <SheetTrigger
                aria-label={
                  (inMore ? `다른 화면 (지금: ${hubs[moreSlot].screens.find((t) => t.href === pathname)?.label})` : "다른 화면") +
                  (moreMarked ? `, 결재 대기 ${pending}건` : "")
                }
                title="다른 화면"
                onClick={(e) => {
                  if (suppressClick.current) {
                    e.preventDefault();
                  } else if (inMore) {
                    // Inside it, the slot goes round its screens like any other.
                    e.preventDefault();
                    go(moreSlot);
                  }
                }}
                className={cn(item, lit === moreSlot ? "text-foreground" : "text-dim")}
              >
                {(() => {
                  // Inside 더보기, the slot shows where you are rather than "more".
                  const Icon = inMore ? (ICON[pathname] ?? Ellipsis) : Ellipsis;
                  const name = inMore ? hubs[moreSlot].screens.find((t) => t.href === pathname)?.label : undefined;
                  return (
                    <>
                      <span className="relative">
                        <Icon aria-hidden className={icon} strokeWidth={lit === moreSlot ? 2.1 : 1.7} />
                        {moreMarked ? (
                          <span
                            aria-hidden
                            className="anim-breathe absolute -top-0.5 -right-1.5 size-[6px] rounded-full bg-foreground/80"
                          />
                        ) : null}
                      </span>
                      {word(name ?? hubs[moreSlot].label)}
                    </>
                  );
                })()}
              </SheetTrigger>
              <BottomSheetContent width="full" keepFocus={false} aria-describedby={undefined} className="gap-4">
                <BottomSheetHeader className="pb-1 sm:px-4">
                  <BottomSheetTitle className="pr-0 text-[14px]">다른 화면</BottomSheetTitle>
                </BottomSheetHeader>
                <ScreenList
                  tabs={hubs[moreSlot].screens}
                  pending={pending}
                  onPick={(tab) => {
                    setMore(false);
                    setChosen(moreSlot);
                    if (tab.href !== pathname) startNavigation(() => router.push(tab.href));
                  }}
                />
                <div className="border-t border-edge-soft px-2 pt-2 pb-4">
                  <button
                    type="button"
                    onClick={() => void signOut()}
                    className="flex min-h-12 w-full items-center gap-3 rounded-xl px-3 text-[14.5px] text-dim outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    <LogOut aria-hidden className="size-[18px]" strokeWidth={1.8} />
                    로그아웃
                  </button>
                </div>
              </BottomSheetContent>
            </Sheet>
          ) : null}
        </div>
      </div>
      {menu ? (
        <div
          ref={menuBox}
          role="menu"
          aria-label={`${hubs[menu.slot].label}의 화면`}
          className="liquid-glass pointer-events-auto origin-bottom animate-in rounded-3xl p-1.5 duration-150 fade-in-0 zoom-in-95 motion-reduce:animate-none"
          // Inline: .liquid-glass sets its own position and next to no paint,
          // and words read over a page need more than the bar's icons do.
          style={{
            position: "absolute",
            background: "color-mix(in oklab, var(--popover) 96%, transparent)",
            left: menu.left,
            width: MENU_WIDTH,
            bottom: "calc(max(env(safe-area-inset-bottom), 0.5rem) + 4.25rem)",
          }}
        >
          {hubs[menu.slot].screens.map((tab, i) => {
            const Icon = ICON[tab.href] ?? Ellipsis;
            return (
              <Link
                key={tab.href}
                href={tab.href}
                role="menuitem"
                draggable={false}
                aria-current={tab.href === pathname ? "page" : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  openScreen(menu.slot, tab.href);
                }}
                className={cn(
                  "flex h-12 items-center gap-2.5 rounded-[1.1rem] px-3 text-[14.5px] transition-colors duration-75 outline-none select-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  i === menu.at ? "bg-glass-raised text-foreground" : tab.href === pathname ? "text-foreground" : "text-dim",
                )}
              >
                <Icon aria-hidden className="size-[18px]" strokeWidth={i === menu.at ? 2.1 : 1.8} />
                {tab.label}
                {pending > 0 && tab.href === pendingHome ? (
                  <span aria-hidden className="anim-breathe size-[6px] rounded-full bg-foreground/80" />
                ) : null}
              </Link>
            );
          })}
        </div>
      ) : null}
    </nav>
  );
}

/** A sheet's list of screens, one row each: 더보기's, and the menu below. */
function ScreenList({
  tabs,
  pending,
  onPick,
}: {
  tabs: Tab[];
  pending: number;
  onPick: (tab: Tab) => void;
}) {
  const pathname = usePathname();
  return (
    <div className="px-2 pb-4">
      {tabs.map((tab) => {
        const Icon = ICON[tab.href] ?? Ellipsis;
        const active = pathname === tab.href;
        const marked = pending > 0 && tab.href === pendingHome;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            prefetch
            onClick={(e) => {
              e.preventDefault();
              onPick(tab);
            }}
            aria-current={active ? "page" : undefined}
            aria-label={marked ? `${tab.label}, 결재 대기 ${pending}건` : undefined}
            className={cn(
              "flex min-h-12 items-center gap-3 rounded-xl px-3 text-[14.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              active ? "bg-glass-raised text-foreground" : "text-dim",
            )}
          >
            <Icon aria-hidden className="size-[18px]" strokeWidth={1.8} />
            {tab.label}
            {marked ? <span aria-hidden className="anim-breathe size-[6px] rounded-full bg-foreground/80" /> : null}
          </Link>
        );
      })}
    </div>
  );
}

/**
 * The way to another screen while the tab bar is put away - a running workout
 * puts it away, so the set being lifted has the bottom of the screen and a
 * tap meant for 완료 cannot land on a tab. Two taps instead of one, which is
 * the point. A phone's only: a wide screen has the links in the header.
 */
export function ScreenMenu({ pending }: { pending: number }) {
  const pathname = usePathname();
  const router = useRouter();
  const role = useRole();
  const [open, setOpen] = useState(false);
  const tabs = hubsFor(role).flatMap((hub) => hub.screens);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger
        aria-label={pending > 0 ? `다른 화면, 결재 대기 ${pending}건` : "다른 화면"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:hidden"
      >
        <LayoutGrid aria-hidden className="size-[18px]" strokeWidth={1.8} />
        {pending > 0 ? (
          <span aria-hidden className="anim-breathe absolute top-2.5 right-2 size-[6px] rounded-full bg-foreground/80" />
        ) : null}
      </SheetTrigger>
      <BottomSheetContent width="full" keepFocus={false} aria-describedby={undefined} className="gap-4">
        <BottomSheetHeader className="pb-1 sm:px-4">
          <BottomSheetTitle className="pr-0 text-[14px]">다른 화면</BottomSheetTitle>
        </BottomSheetHeader>
        <ScreenList
          tabs={tabs}
          pending={pending}
          onPick={(tab) => {
            setOpen(false);
            if (tab.href !== pathname) router.push(tab.href);
          }}
        />
      </BottomSheetContent>
    </Sheet>
  );
}

// Drawn again only when what they show changes: the thread above them
// renders on every frame of an answer.
export const Header = memo(HeaderView);
export const TabBar = memo(TabBarView);
