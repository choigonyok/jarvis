"use client";

import { useLayoutEffect, useRef, useState } from "react";
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
 * The header itself stays solid; only the phone's floating tab bar blurs what
 * is behind it. Two backdrop-filter bars on one screen sample each other in
 * Chrome, which once showed as the tab labels ghosting into the header - so
 * the header's tab pill is the same glass, without the blur.
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
    <header className="inset-x-safe shrink-0 border-b border-edge-soft bg-background">
      <div className="mx-auto flex h-13 w-full max-w-[52rem] items-center justify-between px-4 sm:h-14 sm:px-8">
        <div className="flex min-w-0 items-center gap-4">
          <h1 className="shrink-0 text-[15px] font-semibold tracking-tight text-foreground">
            Jarvis
          </h1>
          {/* The same links, in the bar below, on a phone. */}
          <GlassTabs tabs={tabs} pathname={pathname} />
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
 * The wide header's tabs: one capsule of the same glass as the phone's bar,
 * with the lens under the current page. Tabs differ in width, so the lens is
 * measured onto its tab rather than stepped by a fixed amount.
 */
function GlassTabs({ tabs, pathname }: { tabs: Tab[]; pathname: string }) {
  const refs = useRef(new Map<string, HTMLAnchorElement>());
  const [lens, setLens] = useState<{ x: number; w: number } | null>(null);

  useLayoutEffect(() => {
    const el = refs.current.get(pathname);
    // Measured after layout; the state is the measurement, not derived data.
    setLens(el ? { x: el.offsetLeft, w: el.offsetWidth } : null);
  }, [pathname, tabs]);

  return (
    <nav
      aria-label="화면"
      className="liquid-glass-flat scrollbar-none relative hidden min-w-0 items-center overflow-x-auto rounded-full p-1 sm:flex"
    >
      <span
        aria-hidden
        className="liquid-lens pointer-events-none absolute top-1 bottom-1 left-0 rounded-full"
        style={{
          width: lens?.w ?? 0,
          transform: `translateX(${lens?.x ?? 0}px)`,
          opacity: lens ? 1 : 0,
        }}
      />
      {tabs.map((tab) => {
        const active = pathname === tab.href;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            ref={(el) => {
              if (el) refs.current.set(tab.href, el);
              else refs.current.delete(tab.href);
            }}
            aria-current={active ? "page" : undefined}
            className={cn(
              "relative shrink-0 rounded-full px-3 py-1.5 text-[13px] whitespace-nowrap transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              active ? "text-foreground" : "text-faint hover:text-dim",
            )}
          >
            {tab.label}
          </Link>
        );
      })}
    </nav>
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
  const role = useRole();
  const [more, setMore] = useState(false);

  // 더보기 for a single tab is a sheet with one row; put that tab in the bar.
  let mainTabs = visible(primary, role);
  let moreTabs = visible(secondary, role);
  if (moreTabs.length === 1) {
    mainTabs = [...mainTabs, ...moreTabs];
    moreTabs = [];
  }
  const inMore = moreTabs.some((tab) => tab.href === pathname);
  const slots = mainTabs.length + (moreTabs.length > 0 ? 1 : 0);
  const at = inMore ? mainTabs.length : mainTabs.findIndex((tab) => tab.href === pathname);

  const item =
    "tap relative z-10 flex flex-1 flex-col items-center justify-center gap-0.5 rounded-full outline-none focus-visible:ring-3 focus-visible:ring-ring/50";

  return (
    <nav
      aria-label="화면"
      data-tabbar
      data-hidden={hidden || undefined}
      className={cn(
        "pointer-events-none fixed inset-x-0 bottom-0 z-40 transition-[transform,opacity] duration-300 ease-out motion-reduce:transition-none sm:hidden",
        hidden ? "translate-y-[140%] opacity-0" : "translate-y-0 opacity-100",
      )}
      // Off the edges like the system bar: a gutter on each side, never inside
      // the rounded corners or under the home indicator.
      style={{
        paddingBottom: "max(env(safe-area-inset-bottom), 0.5rem)",
        paddingLeft: "max(env(safe-area-inset-left), 1.25rem)",
        paddingRight: "max(env(safe-area-inset-right), 1.25rem)",
      }}
    >
      <div className="liquid-glass pointer-events-auto relative mx-auto flex h-16 max-w-[24rem] rounded-full p-1.5">
        <div className="relative flex flex-1">
          {/* The lens: one slot wide, slid to the current one. */}
          <span
            aria-hidden
            className="liquid-lens pointer-events-none absolute inset-y-0 left-0 rounded-full"
            style={{
              width: `${100 / slots}%`,
              transform: `translateX(${Math.max(at, 0) * 100}%)`,
              opacity: at >= 0 ? 1 : 0,
            }}
          />
          {mainTabs.map((tab) => {
            const active = pathname === tab.href;
            const Icon = ICON[tab.href] ?? Ellipsis;
            // Only the surface that renders the queue wears the mark, so it
            // reads as "there is something to decide over there" rather than
            // as decoration repeated five times.
            const marked = pending > 0 && tab.href === pendingHome;
            return (
              <Link
                key={tab.href}
                href={tab.href}
                aria-current={active ? "page" : undefined}
                className={cn(item, active ? "text-foreground" : "text-dim")}
              >
                <span className="relative">
                  <Icon aria-hidden className="size-[21px]" strokeWidth={active ? 2.1 : 1.7} />
                  {marked ? (
                    <span
                      role="status"
                      aria-label={`결재 대기 ${pending}건`}
                      className="anim-breathe absolute -top-0.5 -right-1.5 size-[6px] rounded-full bg-foreground/80"
                    />
                  ) : null}
                </span>
                <span className="text-[10.5px] leading-none font-medium">{tab.label}</span>
              </Link>
            );
          })}

          {moreTabs.length > 0 ? (
            <Sheet open={more} onOpenChange={setMore}>
              <SheetTrigger
                aria-label="다른 화면"
                className={cn(item, inMore ? "text-foreground" : "text-dim")}
              >
                <Ellipsis aria-hidden className="size-[21px]" strokeWidth={inMore ? 2.1 : 1.7} />
                <span className="text-[10.5px] leading-none font-medium">
                  {inMore ? (moreTabs.find((t) => t.href === pathname)?.label ?? "더보기") : "더보기"}
                </span>
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
                        onClick={() => setMore(false)}
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
