"use client";

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  type TouchEvent as ReactTouchEvent,
  type UIEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";
import type { ChatMode } from "@/components/chat/mode-toggle";
import { reducedMotion } from "@/components/chat/transcript";
import { useMessageScroller } from "@/components/ui/message-scroller";
import { onFocusProposal } from "@/lib/resume";
import type { Proposal, Turn } from "@/lib/thread";

/**
 * Where the reader was in the conversation when they left it for another tab:
 * the row at the top of the view, how far into the view it stood, and the
 * newest turn then. Coming back lands there, not at the bottom - unless they
 * had been at the bottom - and what came meanwhile is counted on the way
 * down (새 메시지 N).
 */
let readingSpot: { row: string; offset: number; newest: string | null } | null = null;

/** How long a send holds the view at the bottom, at least, and at most. */
const HOLD_MS = 800;
const HOLD_MAX_MS = 3000;

/** The scroller's moves: to the end, to a row. */
export type Scroller = ReturnType<typeof useMessageScroller>;

/**
 * Hands the scroller's moves to the screen around it: a send from the
 * composer, the 맨 아래로 button and a push landing on a card all live
 * outside the scroller.
 */
export function ScrollerMoves({ toRef }: { toRef: RefObject<Scroller | null> }) {
  const moves = useMessageScroller();
  useLayoutEffect(() => {
    toRef.current = moves;
    return () => {
      if (toRef.current === moves) toRef.current = null;
    };
  }, [toRef, moves]);
  return null;
}

/**
 * How the conversation's view moves, beyond what the scroller does on its
 * own: following the bottom handed back to the reader and taken again, a send
 * held at the bottom while a phone's keyboard comes up, a page of older turns
 * put in front without a jump, the reading spot kept across tabs, a card a push
 * landed on held in the middle, the way back down and what came meanwhile, and
 * the pages above read until a card asked for is among them.
 *
 * The screen spreads `viewport` on the scroller's viewport and `content` on the
 * box inside it, and hands `scroller` to ScrollerMoves inside the provider.
 */
export function useThreadScroll({
  scrollRef,
  turns,
  proposals,
  byId,
  hasMore,
  loadingOlder,
  olderFailed,
  loadOlder,
  mode,
}: {
  scrollRef: RefObject<HTMLDivElement | null>;
  turns: Turn[];
  proposals: Proposal[];
  byId: Map<string, Proposal>;
  hasMore: boolean;
  loadingOlder: boolean;
  olderFailed: boolean;
  loadOlder: () => Promise<void>;
  /** The voice stage builds the scroller anew on its return. */
  mode: ChatMode;
}) {
  // The scroller (ui/message-scroller.tsx) lands on the newest turn before
  // anything is painted and follows the bottom on its own - the steps being
  // taken, a message queued above the composer, an answer as it is written -
  // until the reader scrolls themselves, and again once they are back at the
  // very bottom. Following is decided by what the reader did, not by where
  // the view happens to be: a reader who scrolls a line up to read is not
  // pulled back down by the next turn. Its moves are reached from here.
  const scroller = useRef<Scroller | null>(null);
  // Until when a send holds the view at the bottom (toNewest): one scrollTop
  // does not stick on the phone - iOS puts the view back where it had been
  // within 120ms as the keyboard and the layout move. A finger on the
  // thread ends it.
  const holdUntil = useRef(0);
  // When that send was: the hold is stretched while the view is still moving
  // (iOS momentum, the keyboard) but never past HOLD_MAX_MS from it.
  const holdFrom = useRef(0);
  // Up far enough that the newest turn is out of sight: the way back down is
  // offered (the button over the composer).
  const [away, setAway] = useState(false);
  // The newest turn when the reader was last at the bottom; what Jarvis said
  // after it is what the button counts as new.
  const [seenAt, setSeenAt] = useState<string | null>(null);
  const unseen = useMemo(() => {
    if (!away || !seenAt) return 0;
    const at = turns.findIndex((t) => t.id === seenAt);
    if (at === -1) return 0;
    return turns.slice(at + 1).filter((t) => t.role === "agent").length;
  }, [away, seenAt, turns]);
  // The card a tapped push brought the reader to. While set it is kept in the
  // middle - photos above finishing their layout would push it down - until
  // the reader touches the thread themselves.
  const held = useRef<HTMLElement | null>(null);
  // Whether a finger is on the thread, and the wait for scrolling to settle:
  // the page above is asked for only once the reader has stopped near the
  // top - finger lifted, fling over. On an iPhone the view's position is the
  // system's while a finger or a fling moves it, so the page's height put
  // back mid-fling was overwritten: the view fell into the page just added,
  // was near the top again, asked for the next one - one flick up ran
  // through page after page.
  const touching = useRef(false);
  // When the finger last moved, or came down. A touch whose end never came -
  // the row under it replaced as it was touched (a draft turning into its
  // turn), its touchend dispatched to a node no longer in the page - would
  // otherwise read as a finger forever.
  const touchAt = useRef(0);
  const finger = useCallback(() => touching.current && performance.now() - touchAt.current < 1500, []);
  const settle = useRef<number | undefined>(undefined);
  // Where the finger came down: a move of less than a few pixels up or down
  // from there is not scrolling.
  const touchY = useRef<number | null>(null);
  // When the reader last scrolled the thread themselves (a move up or down,
  // a wheel turn).
  const intentAt = useRef(0);
  // Set while a page of older turns is on its way: how tall the thread was
  // and where the reader was, so once the page is in front of them the view
  // is moved by exactly what was added and nothing seems to jump. This is
  // the screen's and not the scroller's: the scroller tells a page put in
  // front by its first row changing, and the first row is often the day's
  // divider, which goes when older turns of the same day come in front of it
  // - on an iPhone the reader was dropped onto the oldest turn of the new
  // page.
  const above = useRef<{ height: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || !above.current) return;
    const { height, top } = above.current;
    above.current = null;
    const target = el.scrollHeight - height + top;
    el.scrollTop = target;
    // iOS can still be finishing a fling it owns and put its own position
    // back over this one: look again on the next frame.
    requestAnimationFrame(() => {
      if (!finger() && Math.abs(el.scrollTop - target) > 2) el.scrollTop = target;
    });
  }, [turns, finger, scrollRef]);

  // Leaving for another tab: where the reader was. Coming back: there again.
  const newest = useRef<string | null>(null);
  useEffect(() => {
    newest.current = turns.at(-1)?.id ?? null;
  });
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const spot = readingSpot;
    readingSpot = null;
    const row = el && spot ? el.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(spot.row)}"]`) : null;
    if (el && spot && row) {
      // The reader's own place, as a scroll would take it: the scroller lets go of the bottom.
      el.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
      el.scrollTop += row.getBoundingClientRect().top - el.getBoundingClientRect().top - spot.offset;
      setSeenAt(spot.newest);
      setAway(true);
    }
    return () => {
      // The view on screen as the reader leaves - voice mode builds it anew -
      // not the one there on arriving.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      const view = scrollRef.current;
      if (!view || view.scrollHeight - view.scrollTop - view.clientHeight < 80) return;
      const top = view.getBoundingClientRect().top;
      for (const r of view.querySelectorAll<HTMLElement>("[data-message-id]")) {
        const box = r.getBoundingClientRect();
        if (box.bottom > top + 1 && r.dataset.messageId) {
          readingSpot = { row: r.dataset.messageId, offset: box.top - top, newest: newest.current };
          break;
        }
      }
    };
  }, [scrollRef]);

  // Back at the very bottom by the reader's own hand: following again. The
  // scroller looks only when the view moves, and a finger or a wheel still
  // pushing at the bottom moves nothing - it would stay let go of, and the
  // answer would grow out of sight under a reader who is right there.
  const wheelEnd = useRef<number | undefined>(undefined);
  // When something in the thread was last opened or closed to be read (a
  // job's 더 보기, a checklist, a notice): the reader took the view then.
  const expanding = useRef(0);
  const repin = useCallback(() => {
    const el = scrollRef.current;
    if (!el || held.current || performance.now() - expanding.current < 1500) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight <= 2) scroller.current?.scrollToEnd();
  }, [scrollRef]);

  // Scrolling up near the top asks for the page above.
  const askOlder = useCallback(
    (el: HTMLElement, tapped = false) => {
      if (!hasMore || loadingOlder || above.current) return;
      // A page that failed is asked again only on a tap, not by every scroll.
      if (olderFailed && !tapped) return;
      if (!tapped && el.scrollTop > 320) return;
      above.current = { height: el.scrollHeight, top: el.scrollTop };
      void loadOlder().then(() => {
        // Nothing came (or it failed): nothing to compensate for.
        window.setTimeout(() => {
          if (above.current) above.current = null;
        }, 1500);
      });
    },
    [hasMore, loadingOlder, olderFailed, loadOlder],
  );

  // A tapped push about a card lands on that card - not the bottom of the
  // thread - and marks it for a moment. From ?proposal= when the tap opened
  // this screen, or from the service worker when it was already open. The
  // card may arrive after the tap (the thread is still loading): it is
  // looked for again as turns and cards come in.
  const wanted = useRef<string | null>(null);
  // Pages above being read for the card: said over the composer meanwhile - the view
  // sat still at the bottom with nothing to show for it.
  const [seeking, setSeeking] = useState(false);
  const focusCard = useCallback(() => {
    const id = wanted.current;
    const card = id ? scrollRef.current?.querySelector<HTMLElement>(`[data-proposal-id="${CSS.escape(id)}"]`) : null;
    if (!card) {
      // Older than what is loaded: its turn is on a page above. The view stays
      // where it is and the turns come in behind it until the card is among them.
      if (id && hasMore && !loadingOlder && !olderFailed && byId.has(id)) {
        setSeeking(true);
        void loadOlder();
      } else if (id && (!hasMore || olderFailed)) {
        // Nowhere further to look.
        wanted.current = null;
        setSeeking(false);
      }
      return;
    }
    wanted.current = null;
    setSeeking(false);
    held.current = card;
    // To its row through the scroller, so it stops following the bottom,
    // then the card itself to the middle - as near as the thread's ends let
    // it: the scroller's own centring would pad the bottom with blank room
    // to put a card just raised in the middle. At once, not smooth: from the
    // bottom of a long thread a smooth scroll is a long ride, and its first
    // frames still read as "at the bottom".
    const row = card.closest<HTMLElement>("[data-message-id]")?.dataset.messageId;
    if (row) scroller.current?.scrollToMessage(row, { align: "nearest" });
    card.scrollIntoView({ block: "center" });
    card.classList.remove("card-flash");
    void card.offsetWidth; // restart the animation on a second tap
    card.classList.add("card-flash");
    const url = new URL(window.location.href);
    if (url.searchParams.has("proposal")) {
      url.searchParams.delete("proposal");
      window.history.replaceState(window.history.state, "", url.pathname + url.search);
    }
  }, [hasMore, loadingOlder, olderFailed, byId, loadOlder, scrollRef]);
  // 결재하기 here: to the card where it stands, not to another screen.
  const goToCard = useCallback(
    (id: string) => {
      wanted.current = id;
      focusCard();
    },
    [focusCard],
  );
  useEffect(() => {
    wanted.current = new URLSearchParams(window.location.search).get("proposal");
    return onFocusProposal((id) => {
      wanted.current = id;
      focusCard();
    });
  }, [focusCard]);
  useEffect(() => {
    if (!wanted.current) return;
    // A frame later: the first rows a page brings are landed at the bottom
    // by the scroller as they come in, and that landing must not come after
    // this one.
    const frame = requestAnimationFrame(focusCard);
    return () => cancelAnimationFrame(frame);
  }, [turns, proposals, loadingOlder, focusCard]);
  // The reader taking over - a touch, a wheel, typing, anywhere on the page
  // (the composer is outside the thread) - lets go of a held card.
  useEffect(() => {
    const release = () => {
      held.current = null;
      holdUntil.current = 0;
    };
    const opts = { capture: true, passive: true } as const;
    document.addEventListener("pointerdown", release, opts);
    document.addEventListener("wheel", release, opts);
    document.addEventListener("keydown", release, true);
    return () => {
      document.removeEventListener("pointerdown", release, opts);
      document.removeEventListener("wheel", release, opts);
      document.removeEventListener("keydown", release, true);
    };
  }, []);

  // A message of yours - typed and sent, a waiting one put into the running
  // turn, one taken back - takes the view to the newest turn, wherever you
  // were reading, and from there it follows what comes back: the steps, the
  // answer as it is written, the turns after it. At once, not smooth: from far
  // up a smooth ride is long, and the message is already on its way.
  const toNewest = useCallback(() => {
    held.current = null;
    scroller.current?.scrollToEnd();
    // Held there frame by frame while the keyboard and the layout settle, and
    // while the view still moves on its own (onScroll stretches it).
    holdFrom.current = performance.now();
    holdUntil.current = holdFrom.current + HOLD_MS;
    const hold = () => {
      const now = scrollRef.current;
      if (!now || performance.now() >= holdUntil.current || finger()) return;
      if (now.scrollHeight - now.scrollTop - now.clientHeight > 1) scroller.current?.scrollToEnd();
      requestAnimationFrame(hold);
    };
    requestAnimationFrame(hold);
  }, [finger, scrollRef]);

  // Down to the newest turn - and staying there as more come.
  const toBottom = useCallback(() => {
    held.current = null;
    const smooth = !reducedMotion();
    scroller.current?.scrollToEnd({ behavior: smooth ? "smooth" : "auto" });
    if (!smooth) return;
    // A smooth ride is stopped by anything else that moves the view - a page
    // of older turns landing in front is put back where the reader was.
    // Where it stopped short and the reader has not taken the view since,
    // the rest of the way is gone at once.
    const asked = performance.now();
    window.setTimeout(() => {
      const el = scrollRef.current;
      if (!el || held.current || finger() || intentAt.current > asked) return;
      if (el.scrollHeight - el.scrollTop - el.clientHeight > 2) scroller.current?.scrollToEnd();
    }, 700);
  }, [finger, scrollRef]);

  // A quote tapped in a bubble goes back to the message it quotes, and marks
  // it the way a tapped push marks a card.
  const jumpTo = useCallback(
    (id: string) => {
      const row = scrollRef.current?.querySelector<HTMLElement>(`[data-turn-id="${CSS.escape(id)}"]`);
      if (!row) return;
      held.current = row;
      scroller.current?.scrollToMessage(id, { align: "nearest" });
      row.scrollIntoView({ block: "center" });
      row.classList.remove("turn-flash");
      void row.offsetWidth;
      row.classList.add("turn-flash");
    },
    [scrollRef],
  );

  // The bottom is the scroller's to keep as the page grows. A card a push
  // landed on is this screen's: photos above it finishing their layout push
  // it down, and the view itself changes height (the bar over the thread
  // folds once the card is in sight), so it is put back in the middle.
  useEffect(() => {
    const el = scrollRef.current;
    const content = el?.firstElementChild;
    if (!el || !content) return;
    const ro = new ResizeObserver(() => {
      held.current?.scrollIntoView({ block: "center" });
    });
    ro.observe(content);
    ro.observe(el);
    return () => ro.disconnect();
    // Again for the scroller the voice stage's return builds.
  }, [mode, scrollRef]);

  // The scroller's viewport: where the reader is, and their hand on it.
  const viewport = {
    onScroll: (e: UIEvent<HTMLDivElement>) => {
      const el = e.currentTarget;
      const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
      // Where the reader is, whoever scrolled: the button is about position.
      setAway(gap > Math.max(320, el.clientHeight * 0.6));
      if (gap < 80) {
        const newest = turns.at(-1)?.id ?? null;
        setSeenAt((prev) => (prev === newest ? prev : newest));
      }
      if (held.current) return; // our own scroll to a card, not the reader's
      // Still moving without a finger after a send - iOS momentum,
      // the keyboard - so the hold lasts until it has stopped.
      const now = performance.now();
      if (now < holdUntil.current && !finger() && gap > 1) {
        holdUntil.current = Math.min(holdFrom.current + HOLD_MAX_MS, Math.max(holdUntil.current, now + 300));
      }
      window.clearTimeout(settle.current);
      const ask = () => {
        if (finger()) settle.current = window.setTimeout(ask, 120);
        else askOlder(el);
      };
      settle.current = window.setTimeout(ask, 180);
    },
    onTouchStart: () => {
      touching.current = true;
      touchAt.current = performance.now();
      holdUntil.current = 0;
    },
    onTouchMove: () => {
      touchAt.current = performance.now();
    },
    onTouchEnd: () => {
      touching.current = false;
      repin();
    },
    onTouchCancel: () => {
      touching.current = false;
      repin();
    },
    onKeyUp: repin,
  };

  // The box inside the viewport. The scroller takes any touch move or wheel
  // turn as the reader scrolling and stops following the bottom. A finger
  // resting on the thread, or a table or a command scrolled sideways, is not
  // that: only a move up or down reaches it.
  const content = {
    onPointerDownCapture: (e: ReactPointerEvent<HTMLDivElement>) => {
      if (!(e.target as Element).closest("[aria-expanded]")) return;
      // Opening or closing something to read it is the reader taking the
      // view, as a scroll is: left to the scroller, the view went on
      // following the bottom as the card grew - and the finger let go at
      // the bottom put it back there - and the head of what was opened
      // went up out of sight. The scroller lets go on the keys it scrolls
      // by; one is said here, not pressed.
      expanding.current = performance.now();
      scrollRef.current?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    },
    onTouchStart: (e: ReactTouchEvent<HTMLDivElement>) => {
      touchY.current = e.touches[0]?.clientY ?? null;
    },
    onTouchMove: (e: ReactTouchEvent<HTMLDivElement>) => {
      const y = e.touches[0]?.clientY;
      if (touchY.current !== null && y !== undefined && Math.abs(y - touchY.current) < 8) {
        e.stopPropagation();
        return;
      }
      intentAt.current = performance.now();
    },
    onWheel: (e: ReactWheelEvent<HTMLDivElement>) => {
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) {
        e.stopPropagation();
        return;
      }
      intentAt.current = performance.now();
      window.clearTimeout(wheelEnd.current);
      wheelEnd.current = window.setTimeout(repin, 150);
    },
  };

  return { scroller, away, unseen, seeking, askOlder, goToCard, toNewest, toBottom, jumpTo, viewport, content };
}
