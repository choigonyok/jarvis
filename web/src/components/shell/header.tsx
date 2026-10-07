"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { LogOut, MoreHorizontal } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import type { Connection } from "@/lib/thread";
import { cn } from "@/lib/utils";

const connectionCopy: Record<Connection, { label: string; dot: string }> = {
  connecting: { label: "연결 중", dot: "bg-faint" },
  open: { label: "연결됨", dot: "bg-online" },
  closed: { label: "연결 끊김", dot: "bg-faint" },
};

type Tab = { href: string; label: string };

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
 * Neither bar is glass. They are flex siblings of the scroller, so nothing
 * ever passes beneath them - and two backdrop-filter bars on one screen
 * sample each other in Chrome, which showed as the tab labels ghosting into
 * the header.
 */
export function Header({
  connection,
  pending,
}: {
  connection: Connection;
  pending: number;
}) {
  const pathname = usePathname();
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
        <div className="flex items-baseline gap-4">
          <h1 className="text-[15px] font-semibold tracking-tight text-foreground">
            Jarvis
          </h1>
          {/* The same links, in the bar below, on a phone. */}
          <nav className="hidden items-baseline gap-3 sm:flex" aria-label="화면">
            {all.map((tab) => {
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
 * The phone's navigation. It sits against the bottom edge, above the home
 * indicator, and hides itself while something is being typed - the keyboard
 * has covered it anyway, and the space it gives back is the space the
 * transcript needs most at that moment.
 */
export function TabBar({
  pending,
  hidden,
}: {
  pending: number;
  hidden?: boolean;
}) {
  const pathname = usePathname();
  const [more, setMore] = useState(false);
  const inMore = secondary.some((tab) => tab.href === pathname);

  return (
    <nav
      aria-label="화면"
      className={cn(
        "inset-x-safe pb-safe shrink-0 border-t border-edge-soft bg-background transition-[height,opacity] duration-200 sm:hidden",
        hidden ? "pointer-events-none h-0 overflow-hidden opacity-0" : "opacity-100",
      )}
    >
      <div className="flex items-stretch justify-around px-1">
        {primary.map((tab) => {
          const active = pathname === tab.href;
          // Only the surface that renders the queue wears the mark, so it
          // reads as "there is something to decide over there" rather than as
          // decoration repeated five times.
          const marked = pending > 0 && tab.href === pendingHome;
          return (
            <Link
              key={tab.href}
              href={tab.href}
              aria-current={active ? "page" : undefined}
              className={cn(
                "tap flex flex-1 items-center justify-center rounded-lg text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                active ? "text-foreground" : "text-faint",
              )}
            >
              <span className="relative">
                {tab.label}
                {marked ? (
                  <span
                    role="status"
                    aria-label={`결재 대기 ${pending}건`}
                    className="anim-breathe absolute top-1/2 -right-2.5 size-[5px] -translate-y-1/2 rounded-full bg-dim"
                  />
                ) : null}
              </span>
            </Link>
          );
        })}

        <Sheet open={more} onOpenChange={setMore}>
          <SheetTrigger
            aria-label="다른 화면"
            className={cn(
              "tap flex flex-1 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              // Wears the current page's state, so being inside 더보기 does not
              // read as being nowhere.
              inMore ? "text-foreground" : "text-faint",
            )}
          >
            <MoreHorizontal aria-hidden className="size-[18px]" />
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
              {secondary.map((tab) => (
                <Link
                  key={tab.href}
                  href={tab.href}
                  onClick={() => setMore(false)}
                  aria-current={pathname === tab.href ? "page" : undefined}
                  className={cn(
                    "flex min-h-12 items-center rounded-lg px-3 text-[14.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                    pathname === tab.href
                      ? "bg-glass-raised text-foreground"
                      : "text-dim",
                  )}
                >
                  {tab.label}
                </Link>
              ))}
            </div>
          </SheetContent>
        </Sheet>
      </div>
    </nav>
  );
}
