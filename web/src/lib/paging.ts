"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { onFocusWanted, wantedFocus } from "@/lib/focus";

/**
 * A long list, a page at a time (components/ui/pager.tsx draws the pages).
 *
 * Every list that grows with time - charges, cards, notifications, runs - is
 * shown twenty rows at a time, so a phone does not scroll past a month of
 * rows to reach the end of the screen. The page starts over when what the
 * list shows changes (`reset`: the month, a filter, a search), and a row a
 * notification points at (?focus=, lib/focus.ts) opens the page it is on.
 *
 * Lists that are a picture of now rather than a record - the status checks,
 * the holdings, today's departures - are not split: a page of them would hide
 * part of the state they are there to show.
 */

export const PAGE_SIZE = 20;

export type Paged<T> = {
  /** This page's rows. */
  items: T[];
  page: number;
  pageCount: number;
  total: number;
  /** 1-based and inclusive: rows 21–40. */
  from: number;
  to: number;
  go: (page: number) => void;
  /** The id to put on the list's top: turning a page brings it into view. */
  anchor: string;
};

export function usePaged<T>(
  all: T[],
  opts: {
    size?: number;
    /** What the list is showing - a string or a number: when it changes, back to the first page. */
    reset?: string | number;
    /** The row's data-focus id, for a row a notification points at. */
    focusKey?: (item: T) => string;
  } = {},
): Paged<T> {
  const size = opts.size ?? PAGE_SIZE;
  const reset = opts.reset ?? "";
  const [state, setState] = useState({ page: 1, reset });
  const anchor = useId();
  const pageCount = Math.max(1, Math.ceil(all.length / size));
  // A list that changed starts at its first page; one that shrank keeps the last page there is.
  const page = Math.min(state.reset === reset ? state.page : 1, pageCount);

  // The same page again is the same state: no render for nothing.
  const turn = useCallback(
    (next: number) => setState((s) => (s.page === next && s.reset === reset ? s : { page: next, reset })),
    [reset],
  );

  const go = useCallback(
    (next: number) => {
      turn(next);
      const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      document.getElementById(anchor)?.scrollIntoView({ block: "start", behavior: reduce ? "auto" : "smooth" });
    },
    [turn, anchor],
  );

  const keyOf = useRef(opts.focusKey);
  useEffect(() => {
    keyOf.current = opts.focusKey;
  });
  useEffect(() => {
    const open = (id: string) => {
      const key = keyOf.current;
      const at = key ? all.findIndex((item) => key(item) === id) : -1;
      if (at >= 0) turn(Math.floor(at / size) + 1);
    };
    const id = wantedFocus();
    // The row may be on another page, or the rows may have only now arrived:
    // the page it is on opens, and focusSection finds it there.
    if (id) open(id);
    return onFocusWanted(open);
  }, [all, size, turn]);

  const start = (page - 1) * size;
  return {
    items: all.slice(start, start + size),
    page,
    pageCount,
    total: all.length,
    from: all.length === 0 ? 0 : start + 1,
    to: Math.min(start + size, all.length),
    go,
    anchor,
  };
}
