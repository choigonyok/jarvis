"use client";

import { useEffect, useRef, useState, useTransition, type ReactNode } from "react";
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
  Wallet,
} from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Notifications } from "@/components/shell/notifications";
import { useRole } from "@/components/shell/role";
import { UsageBars } from "@/components/shell/usage-bars";
import { focusSection, forgetFocus, onFocusRequest } from "@/lib/focus";
import { GUEST_PAGES, type Role } from "@/lib/role";
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
  "/photos": Images,
  "/edit": Brush,
  "/life": BookUser,
  "/location": MapPin,
  "/calendar": CalendarDays,
  "/screen": MonitorPlay,
  "/kakao": MessagesSquare,
  "/status": Activity,
};

/**
 * Nine surfaces is more than a thumb bar can hold - five is the limit before
 * the targets get too narrow to hit reliably. So the phone keeps the ones you
 * open on purpose, and the ones you only reach for once you are already
 * looking at something - 자산, the calendar, the agent's screen, KakaoTalk,
 * 상태 -
 * move behind 더보기. A wide header has room for all of them at once.
 */
const primary: Tab[] = [
  { href: "/", label: "대화" },
  { href: "/workout", label: "운동" },
  { href: "/record", label: "기록" },
];

// all 이 이 배열을 그대로 펼치므로, 넓은 화면의 읽는 순서가 대화 → 운동 →
// 작업 → 자산 → 가계부로 이어진다: 맡긴 일 다음에 "쌓아두는 것"끼리 붙어 있다.
const secondary: Tab[] = [
  // 맡긴 일이 지금 어디까지 왔는지. 대화 바로 다음에 읽힌다.
  { href: "/jobs", label: "작업" },
  { href: "/assets", label: "자산" },
  { href: "/spending", label: "가계부" },
  { href: "/calendar", label: "캘린더" },
  { href: "/photos", label: "사진" },
  // 사진 다음에 그림을 고치는 곳. PhotoCraft 가 보는 사람의 브라우저 안에서 돈다.
  { href: "/edit", label: "편집" },
  // Jarvis 가 내 생활에서 읽어 낸 것: 나에 대한 노트, 회고한 장소, 가진 물건, 식사.
  { href: "/life", label: "생활" },
  // 하루 동안 어디를 다녔는지: 폰이 보낸 위치를 지도 위에 되감아 보고, 끝은 지금.
  { href: "/location", label: "위치" },
  { href: "/screen", label: "화면" },
  { href: "/kakao", label: "카톡" },
  // 맨 끝. 매일 여는 곳이 아니라 뭔가 이상할 때 찾아오는 곳이다.
  { href: "/status", label: "상태" },
];

/** Reading order on a wide screen: talk, the things you keep, then the queue. */
const all: Tab[] = [
  { href: "/", label: "대화" },
  { href: "/workout", label: "운동" },
  ...secondary,
  { href: "/record", label: "기록" },
];

/** The tabs a role is shown. The middleware is what actually refuses the rest. */
const visible = (tabs: Tab[], role: Role) =>
  role === "guest" ? tabs.filter((t) => GUEST_PAGES.has(t.href)) : tabs;

/**
 * The phone's bar, icons only. The operator's daily surfaces - the
 * conversation, money in and money out - always sit in it; the guest has
 * five tabs and no 더보기.
 */
const OWNER_BAR = ["/", "/jobs", "/assets", "/spending", "/calendar", "/workout", "/record"];
const OWNER_MORE = ["/life", "/location", "/photos", "/edit", "/screen", "/kakao", "/status"];
const GUEST_BAR = ["/", "/calendar", "/location", "/workout", "/edit"];

const byHref = new Map([...primary, ...secondary].map((t) => [t.href, t]));
const pick = (hrefs: string[]) => hrefs.flatMap((h) => (byHref.has(h) ? [byHref.get(h)!] : []));

/** A role's screens on a phone: the ones in the bar, and the ones behind 더보기. */
function screensFor(role: Role) {
  return {
    bar: pick(role === "guest" ? GUEST_BAR : OWNER_BAR),
    more: role === "guest" ? [] : pick(OWNER_MORE),
  };
}

/** Where a waiting decision is announced: the surface that lists them by name. */
const pendingHome = "/record";

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
export function Header({
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
  const tabs = visible(all, role);
  const router = useRouter();
  const status = connectionCopy[connection];

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

  async function signOut() {
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
      <div className="mx-auto flex h-13 w-full max-w-[52rem] items-center justify-between px-4 sm:h-14 sm:px-8">
        <div className="flex min-w-0 items-baseline gap-3">
          <h1
            className="shrink-0 text-[15px] font-semibold tracking-tight text-foreground select-none"
            onClick={secretTap}
          >
            Jarvis
          </h1>
          {/* The same links, in the bar below, on a phone. A row that runs
              out of room scrolls inside itself; a label never breaks in two.
              Under a finger (an iPad, a phone on its side) a label is a 44px
              target; with a mouse it is only the word. */}
          <nav className="scrollbar-none hidden min-w-0 items-baseline overflow-x-auto sm:flex mouse:gap-[9px]" aria-label="화면">
            {tabs.map((tab) => {
              const active = pathname === tab.href;
              return (
                <Link
                  key={tab.href}
                  href={tab.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "inline-flex h-11 shrink-0 items-center rounded-sm px-1.5 text-[13px] whitespace-nowrap transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-auto mouse:px-0",
                    active ? "text-foreground" : "text-faint hover:text-dim",
                  )}
                >
                  {tab.label}
                </Link>
              );
            })}
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
            {/* The dot alone carries this on a phone; the word is the luxury
                of a wide row. */}
            <span className="hidden sm:inline">{status.label}</span>
            <span className="sr-only sm:hidden">{status.label}</span>
          </span>
          <p className="hidden text-[11.5px] text-faint lg:block" aria-live="polite">
            {pending > 0 ? `결재 대기 ${pending}건` : "결재 대기 없음"}
          </p>
          <button
            type="button"
            onClick={() => void signOut()}
            aria-label="로그아웃"
            className="-mr-2 flex h-11 min-w-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:mr-0 sm:px-1.5 sm:text-[11.5px] mouse:h-auto mouse:min-w-0 mouse:px-0"
          >
            <LogOut aria-hidden className="size-4 sm:hidden" />
            <span className="hidden sm:inline">로그아웃</span>
          </button>
        </div>
      </div>
    </header>
  );
}

/**
 * The phone's navigation: a capsule of glass floating over the page, lifted
 * off the edges and the home indicator, the way iOS 26 draws its tab bar. The
 * page scrolls beneath it (see --tabbar-space in globals.css, which gives the
 * scrollers room at the end). A lens of brighter glass sits under the current
 * tab and slides when you tap another.
 *
 * It hides while something is being typed - the keyboard has covered it
 * anyway, and the space it gives back is what the transcript needs most then.
 */
export function TabBar({
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

  // Icons only, so more of them fit: everything a person opens every day is
  // one tap away, and only the rarely visited wait behind 더보기.
  const { bar: mainTabs, more: moreTabs } = screensFor(role);
  const moreSlot = moreTabs.length > 0 ? mainTabs.length : -1;

  const inMore = moreTabs.some((tab) => tab.href === pathname);
  const slots = mainTabs.length + (moreTabs.length > 0 ? 1 : 0);
  const at = inMore ? moreSlot : mainTabs.findIndex((tab) => tab.href === pathname);

  // Where the lens is. It moves the moment a tab is chosen, not when the page
  // has loaded: the bar answers the finger, and the page follows when it is
  // ready (the old one stays up until then - a transition, not a blank).
  const [chosen, setChosen] = useState(at);
  useEffect(() => {
    // The route settled (or changed from elsewhere): the lens follows it.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setChosen(at);
  }, [at]);

  // Every tab is prefetched, so most switches have nothing left to wait for.
  useEffect(() => {
    for (const tab of [...mainTabs, ...moreTabs]) router.prefetch(tab.href);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [role]);

  function go(slot: number) {
    if (slot === moreSlot) {
      setMore(true);
      return;
    }
    const tab = mainTabs[slot];
    if (!tab) return;
    setChosen(slot);
    if (tab.href !== pathname) startNavigation(() => router.push(tab.href));
  }

  // Dragging along the bar, as on iOS: the lens follows the finger and the tab
  // under it on release is the one opened.
  const track = useRef<HTMLDivElement>(null);
  const drag = useRef<{ startX: number; moved: boolean; width: number; left: number } | null>(null);
  // Where the finger is along the bar, and the slot under it - both worked out
  // in the pointer handler, which is where the bar's width is known.
  const [held, setHeld] = useState<{ x: number; slot: number } | null>(null);
  const suppressClick = useRef(false);
  const slotAt = (x: number, width: number) =>
    Math.min(slots - 1, Math.max(0, Math.floor((x / width) * slots)));

  function onPointerDown(e: React.PointerEvent<HTMLDivElement>) {
    const box = track.current?.getBoundingClientRect();
    if (!box) return;
    drag.current = { startX: e.clientX, moved: false, width: box.width, left: box.left };
  }
  function onPointerMove(e: React.PointerEvent<HTMLDivElement>) {
    const d = drag.current;
    if (!d) return;
    if (!d.moved && Math.abs(e.clientX - d.startX) < 8) return;
    if (!d.moved) {
      d.moved = true;
      // From here the bar owns the gesture: the page does not scroll, and the
      // tab the press started on is not "clicked" on release.
      e.currentTarget.setPointerCapture(e.pointerId);
    }
    const x = Math.min(d.width, Math.max(0, e.clientX - d.left));
    setHeld({ x, slot: slotAt(x, d.width) });
  }
  function onPointerUp(e: React.PointerEvent<HTMLDivElement>) {
    const d = drag.current;
    drag.current = null;
    if (!d?.moved) return;
    suppressClick.current = true;
    setTimeout(() => (suppressClick.current = false), 0);
    setHeld(null);
    go(slotAt(Math.min(d.width, Math.max(0, e.clientX - d.left)), d.width));
  }
  function onPointerCancel() {
    drag.current = null;
    setHeld(null);
  }

  const dragging = held !== null;
  const lit = held ? held.slot : chosen;
  const slotWidth = 100 / slots;

  // A slot is an equal share of the bar rather than a fixed 44px: the bar's
  // width (below) is what keeps the targets, and on a narrow phone each share
  // gives a little instead of pushing the last one off the screen.
  const item =
    "liquid-press relative z-10 flex min-h-11 min-w-0 flex-1 items-center justify-center rounded-full outline-none focus-visible:ring-3 focus-visible:ring-ring/50";

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
      <div
        className="liquid-glass pointer-events-auto relative mx-auto flex h-[3.75rem] w-full rounded-full p-1.5"
        // 44px a tab and no wider - a short bar for a short list, three icons
        // do not stretch edge to edge - and never wider than the space between
        // the gutters. Eight tabs at a fixed 44px were 364px, more than a
        // 390px phone has (358): the bar ran into the right gutter, and on a
        // 360px phone cut 더보기 off.
        style={{ maxWidth: `calc(${slots} * 2.75rem + 0.75rem)` }}
      >
        <div
          ref={track}
          className="relative flex flex-1 touch-none select-none"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerCancel}
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
          {mainTabs.map((tab, slot) => {
            const active = slot === lit;
            const Icon = ICON[tab.href] ?? Ellipsis;
            // Only the surface that renders the queue wears the mark, so it
            // reads as "there is something to decide over there".
            const marked = pending > 0 && tab.href === pendingHome;
            return (
              <Link
                key={tab.href}
                href={tab.href}
                prefetch
                draggable={false}
                aria-label={marked ? `${tab.label}, 결재 대기 ${pending}건` : tab.label}
                title={tab.label}
                aria-current={pathname === tab.href ? "page" : undefined}
                onClick={(e) => {
                  // Navigation is ours, so the lens can move before the page does.
                  e.preventDefault();
                  if (suppressClick.current) return;
                  go(slot);
                }}
                className={cn(item, active ? "text-foreground" : "text-dim")}
              >
                <span className="relative">
                  <Icon aria-hidden className="size-[22px]" strokeWidth={active ? 2.1 : 1.7} />
                  {marked ? (
                    <span
                      aria-hidden
                      className="anim-breathe absolute -top-0.5 -right-1.5 size-[6px] rounded-full bg-foreground/80"
                    />
                  ) : null}
                </span>
              </Link>
            );
          })}

          {moreTabs.length > 0 ? (
            <Sheet open={more} onOpenChange={setMore}>
              <SheetTrigger
                aria-label={inMore ? `다른 화면 (지금: ${moreTabs.find((t) => t.href === pathname)?.label})` : "다른 화면"}
                title="다른 화면"
                onClick={(e) => {
                  if (suppressClick.current) e.preventDefault();
                }}
                className={cn(item, lit === moreSlot ? "text-foreground" : "text-dim")}
              >
                {(() => {
                  // Inside 더보기, the slot shows where you are rather than "more".
                  const Icon = inMore ? (ICON[pathname] ?? Ellipsis) : Ellipsis;
                  return <Icon aria-hidden className="size-[22px]" strokeWidth={lit === moreSlot ? 2.1 : 1.7} />;
                })()}
              </SheetTrigger>
              <SheetContent
                side="bottom"
                className="inset-x-safe pb-safe border-edge-soft bg-background"
              >
                <SheetHeader className="px-4 pt-4 pb-1">
                  <SheetTitle className="text-[14px] font-medium text-foreground">
                    다른 화면
                  </SheetTitle>
                </SheetHeader>
                <ScreenList
                  tabs={moreTabs}
                  pending={pending}
                  onPick={(tab) => {
                    setMore(false);
                    setChosen(moreSlot);
                    if (tab.href !== pathname) startNavigation(() => router.push(tab.href));
                  }}
                />
              </SheetContent>
            </Sheet>
          ) : null}
        </div>
      </div>
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
  const { bar, more } = screensFor(role);
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
      <SheetContent side="bottom" className="inset-x-safe pb-safe border-edge-soft bg-background">
        <SheetHeader className="px-4 pt-4 pb-1">
          <SheetTitle className="text-[14px] font-medium text-foreground">다른 화면</SheetTitle>
        </SheetHeader>
        <ScreenList
          tabs={[...bar, ...more]}
          pending={pending}
          onPick={(tab) => {
            setOpen(false);
            if (tab.href !== pathname) router.push(tab.href);
          }}
        />
      </SheetContent>
    </Sheet>
  );
}
