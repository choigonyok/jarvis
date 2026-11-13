"use client";

import { memo, useEffect, useRef, useState, useTransition } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Ellipsis, LogOut } from "lucide-react";
import { BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Sheet, SheetTrigger } from "@/components/ui/sheet";
import { HUB_ICON, SCREEN_ICON, lastIn } from "@/components/shell/places";
import { useRole } from "@/components/shell/role";
import { ScreenList } from "@/components/shell/screen-menu";
import { useSignOut } from "@/components/shell/sign-out";
import { hubOf, hubsFor, pendingHome } from "@/lib/hubs";
import { cn } from "@/lib/utils";

/** How long a finger rests on a place before its screens come up. */
const HOLD_MS = 350;
const MENU_WIDTH = 148;

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
  const worded = hubs.length <= 6;
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
    "liquid-press relative z-10 flex min-h-11 min-w-0 flex-1 flex-col items-center justify-center gap-[3px] rounded-full focus-ring";
  const icon = worded ? "size-5" : "size-[22px]";
  const word = (label: string) => (worded ? <span className="text-tiny leading-none">{label}</span> : null);

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
            const Icon = HUB_ICON[hub.id];
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
                  const Icon = inMore ? (SCREEN_ICON[pathname] ?? Ellipsis) : Ellipsis;
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
                  <BottomSheetTitle className="pr-0 text-body">다른 화면</BottomSheetTitle>
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
                    className="flex min-h-12 w-full items-center gap-3 rounded-xl px-3 text-body text-dim focus-ring"
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
            const Icon = SCREEN_ICON[tab.href] ?? Ellipsis;
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
                  "flex h-12 items-center gap-2.5 rounded-[1.1rem] px-3 text-body transition-colors duration-75 select-none focus-ring",
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

// Drawn again only when what it shows changes: the thread above it renders
// on every frame of an answer.
export const TabBar = memo(TabBarView);
