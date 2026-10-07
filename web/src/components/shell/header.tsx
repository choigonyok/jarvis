"use client";

import { useEffect, useRef, useState, useTransition } from "react";
import { PROBE_KEY } from "@/components/shell/viewport-probe";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  Activity,
  CalendarDays,
  ClipboardCheck,
  Dumbbell,
  Ellipsis,
  LogOut,
  type LucideIcon,
  MessageCircle,
  MessagesSquare,
  MonitorPlay,
  ReceiptText,
  Wallet,
} from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { useRole } from "@/components/shell/role";
import { GUEST_PAGES, type Role } from "@/lib/role";
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

// 자산이 secondary의 첫 칸인 이유는 all이 이 배열을 그대로 펼치기 때문이다:
// 넓은 화면의 읽는 순서가 대화 → 운동 → 자산 → 가계부로 이어져야 "쌓아두는
// 것"끼리 붙어 있는다.
const secondary: Tab[] = [
  { href: "/assets", label: "자산" },
  { href: "/spending", label: "가계부" },
  { href: "/calendar", label: "캘린더" },
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
 * three tabs and no 더보기.
 */
const OWNER_BAR = ["/", "/assets", "/spending", "/calendar", "/workout", "/record"];
const OWNER_MORE = ["/screen", "/kakao", "/status"];
const GUEST_BAR = ["/", "/calendar", "/workout"];

/** Where a waiting decision is announced: the surface that lists them by name. */
const pendingHome = "/record";

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
}: {
  connection: Connection;
  pending: number;
}) {
  const pathname = usePathname();
  const tabs = visible(all, useRole());
  const router = useRouter();
  const status = connectionCopy[connection];

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
    <header className="inset-x-safe pt-safe shrink-0 border-b border-edge-soft bg-background">
      <div className="mx-auto flex h-13 w-full max-w-[52rem] items-center justify-between px-4 sm:h-14 sm:px-8">
        <div className="flex items-baseline gap-4">
          <h1
            className="text-[15px] font-semibold tracking-tight text-foreground select-none"
            onClick={secretTap}
          >
            Jarvis
          </h1>
          {/* The same links, in the bar below, on a phone. */}
          <nav className="hidden items-baseline gap-3 sm:flex" aria-label="화면">
            {tabs.map((tab) => {
              const active = pathname === tab.href;
              return (
                <Link
                  key={tab.href}
                  href={tab.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "rounded-sm text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                    active ? "text-foreground" : "text-faint hover:text-dim",
                  )}
                >
                  {tab.label}
                </Link>
              );
            })}
          </nav>
        </div>

        <div className="flex items-center gap-3">
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
            className="-mr-2 flex size-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:mr-0 sm:size-auto sm:text-[11.5px]"
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
  const byHref = new Map([...primary, ...secondary].map((t) => [t.href, t]));
  const pick = (hrefs: string[]) => hrefs.flatMap((h) => (byHref.has(h) ? [byHref.get(h)!] : []));
  const mainTabs = pick(role === "guest" ? GUEST_BAR : OWNER_BAR);
  const moreTabs = role === "guest" ? [] : pick(OWNER_MORE);
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

  const item =
    "tap liquid-press relative z-10 flex flex-1 items-center justify-center rounded-full outline-none focus-visible:ring-3 focus-visible:ring-ring/50";

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
        className={cn(
          "liquid-glass pointer-events-auto relative mx-auto flex h-[3.75rem] rounded-full p-1.5",
          // A short bar for a short list: three icons do not stretch edge to edge.
          slots <= 4 ? "max-w-[15rem]" : "max-w-[26rem]",
        )}
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
                <div className="px-2 pb-4">
                  {moreTabs.map((tab) => {
                    const Icon = ICON[tab.href] ?? Ellipsis;
                    const active = pathname === tab.href;
                    return (
                      <Link
                        key={tab.href}
                        href={tab.href}
                        prefetch
                        onClick={(e) => {
                          e.preventDefault();
                          setMore(false);
                          setChosen(moreSlot);
                          if (!active) startNavigation(() => router.push(tab.href));
                        }}
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex min-h-12 items-center gap-3 rounded-xl px-3 text-[14.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                          active ? "liquid-glass-flat text-foreground" : "text-dim",
                        )}
                      >
                        <Icon aria-hidden className="size-[18px]" strokeWidth={1.8} />
                        {tab.label}
                      </Link>
                    );
                  })}
                </div>
              </SheetContent>
            </Sheet>
          ) : null}
        </div>
      </div>
    </nav>
  );
}
