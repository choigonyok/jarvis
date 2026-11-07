"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { Toggle } from "@/components/ui/toggle";

/** Text that is chrome, not something said: times, buttons, the quote copies. */
const SKIP = "button, svg, [data-search-skip]";

/** How long typing has to pause before the thread is searched. */
const SETTLE_MS = 160;

/**
 * Marks drawn at once, around the current hit. A one-letter query on a long
 * thread finds thousands, and every paint walks every mark.
 */
const MARKED = 120;

type Highlights = Map<string, unknown> & { set(name: string, h: unknown): void; delete(name: string): boolean };

function highlights(): Highlights | null {
  const css = globalThis.CSS as unknown as { highlights?: Highlights } | undefined;
  return css?.highlights && typeof (globalThis as { Highlight?: unknown }).Highlight === "function" ? css.highlights : null;
}

function mark(name: string, ranges: Range[]) {
  const hl = highlights();
  if (!hl) return;
  if (ranges.length === 0) {
    hl.delete(name);
    return;
  }
  const H = (globalThis as unknown as { Highlight: new (...r: Range[]) => unknown }).Highlight;
  hl.set(name, new H(...ranges));
}

/** A text node the thread shows, lower-cased once. */
type Entry = { node: Text; text: string };

/** One place the query appears. A Range is made only for the few on screen. */
type Hit = { node: Text; start: number; end: number };

/**
 * Every text node the thread shows. Chrome is passed over a whole subtree at
 * a time rather than asked of each text node, and the result is kept until
 * the thread changes - each new query only scans the strings.
 */
function textOf(root: HTMLElement): Entry[] {
  const out: Entry[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
    acceptNode: (node) =>
      node.nodeType === Node.TEXT_NODE
        ? NodeFilter.FILTER_ACCEPT
        : (node as Element).matches(SKIP)
          ? NodeFilter.FILTER_REJECT
          : NodeFilter.FILTER_SKIP,
  });
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = (node.nodeValue ?? "").toLocaleLowerCase("ko-KR");
    if (text.trim()) out.push({ node: node as Text, text });
  }
  return out;
}

/** Every place `query` appears, newest first. */
function find(entries: Entry[], query: string): Hit[] {
  const q = query.toLocaleLowerCase("ko-KR");
  const out: Hit[] = [];
  for (const { node, text } of entries) {
    for (let at = text.indexOf(q); at !== -1; at = text.indexOf(q, at + q.length)) {
      out.push({ node, start: at, end: at + q.length });
    }
  }
  return out.reverse();
}

/** A hit as a Range, or null when its text has changed since it was found. */
function rangeOf(hit: Hit): Range | null {
  if (!hit.node.isConnected || hit.end > hit.node.length) return null;
  const r = document.createRange();
  r.setStart(hit.node, hit.start);
  r.setEnd(hit.node, hit.end);
  return r;
}

/**
 * Searching the conversation, the way a messenger does it: type, and the
 * newest place it was said comes into view with the places around it marked;
 * the arrows walk back through older ones. What is searched is what the
 * thread shows - the markdown as rendered, the cards' words - so a hit is
 * always something you can see when you land on it.
 *
 * Marks are the CSS Custom Highlight API: nothing is wrapped or re-rendered,
 * so the thread keeps its own DOM. Where the API is missing the arrows still
 * walk, and the line the hit is on is brought to the middle.
 */
/** The thread above what is loaded, as useThread has it. */
export type Above = { hasMore: boolean; failed: boolean; load: () => Promise<unknown> };

/**
 * How many pages a 더 찾기 reads before it stops to ask again: a word said
 * once three weeks ago is a few pages up, and a word never said is every page
 * there is.
 */
const PAGES_A_LOOK = 6;

export function useThreadSearch(root: RefObject<HTMLElement | null>, above?: Above) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[]>(NONE);
  const [at, setAt] = useState(0);
  const first = useRef(true);
  // Bumped when the thread's DOM changes under an open search - a new turn, a
  // card decided - so the text is read again rather than left pointing at
  // nodes that are gone.
  const [version, setVersion] = useState(0);
  const index = useRef<{ version: number; entries: Entry[] } | null>(null);
  useEffect(() => {
    const el = root.current;
    if (!open || !el) return;
    let t = 0;
    const mo = new MutationObserver(() => {
      window.clearTimeout(t);
      t = window.setTimeout(() => setVersion((v) => v + 1), 200);
    });
    mo.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      mo.disconnect();
      window.clearTimeout(t);
      // Nothing watches the thread while closed: read it afresh next time.
      index.current = null;
    };
  }, [open, root]);

  const searching = open && query.trim() !== "";
  const live = searching ? hits : NONE;

  // Look again as the query settles and as the thread changes under it.
  useEffect(() => {
    const el = root.current;
    const q = query.trim();
    if (!open || !el || !q) return;
    if (index.current?.version !== version) index.current = { version, entries: textOf(el) };
    const found = find(index.current.entries, q);
    setHits(found);
    setAt((i) => (first.current ? 0 : Math.min(i, Math.max(0, found.length - 1))));
  }, [open, query, version, root]);

  // Mark the hits around the current one, and bring it to the middle.
  useEffect(() => {
    const el = root.current;
    const hit = live[at];
    if (!hit) {
      mark("chat-search", []);
      mark("chat-search-current", []);
      return;
    }
    const from = Math.max(0, Math.min(at - MARKED / 2, live.length - MARKED));
    mark(
      "chat-search",
      live.slice(from, from + MARKED).flatMap((h) => rangeOf(h) ?? []),
    );
    const current = rangeOf(hit);
    mark("chat-search-current", current ? [current] : []);
    if (!el || !current) return;
    const box = current.getBoundingClientRect();
    const frame = el.getBoundingClientRect();
    if (box.height === 0) return;
    const smooth = !first.current && !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    first.current = false;
    el.scrollTo({ top: el.scrollTop + box.top - frame.top - el.clientHeight / 2, behavior: smooth ? "smooth" : "auto" });
  }, [live, at, root]);

  useEffect(
    () => () => {
      mark("chat-search", []);
      mark("chat-search-current", []);
    },
    [],
  );

  // What is shown is only what is loaded, and "없음" there is no word on the
  // whole conversation. Looking further reads the pages above, one at a time,
  // until the word turns up further back - or the conversation's start, or a
  // few pages, after which it asks again.
  const [seeking, setSeeking] = useState(false);
  const aboveRef = useRef(above);
  const totalRef = useRef(0);
  useEffect(() => {
    aboveRef.current = above;
    totalRef.current = live.length;
  });
  const findMore = useCallback(async () => {
    if (seeking) return;
    setSeeking(true);
    const before = totalRef.current;
    try {
      for (let i = 0; i < PAGES_A_LOOK; i++) {
        const a = aboveRef.current;
        // A page that failed is asked again once; failing again, it stops.
        if (!a?.hasMore || (i > 0 && a.failed)) break;
        await a.load();
        // The rows land, and the search reads them again (the observer waits 200ms).
        await new Promise((r) => window.setTimeout(r, 320));
        if (totalRef.current > before) break;
      }
    } finally {
      setSeeking(false);
    }
    // Hits run newest first: the first one found above is the one after the old last.
    if (totalRef.current > before) setAt(before);
  }, [seeking]);

  // The query last asked for: Enter and the pause after typing may both hand
  // in the same one, and only a new one starts again from the newest hit.
  const asked = useRef("");
  const settle = useCallback((q: string) => {
    if (q === asked.current) return;
    asked.current = q;
    first.current = true;
    setQuery(q);
  }, []);

  // The same answer while nothing in it changed: the header holding the search
  // is not drawn again on every frame of an answer.
  const total = live.length;
  const further = Boolean(above?.hasMore);
  const aboveFailed = Boolean(above?.failed);
  return useMemo(
    () => ({
      open,
      query,
      total,
      at: total ? at : 0,
      /** More of the conversation is above what was searched. */
      further,
      aboveFailed,
      seeking,
      findMore,
      /** The query once typing has paused (SearchBar holds what is being typed). */
      setQuery: settle,
      show: () => {
        first.current = true;
        setOpen(true);
      },
      close: () => {
        setOpen(false);
        asked.current = "";
        setQuery("");
        setHits(NONE);
        setAt(0);
      },
      /** Older: further up the thread - past the oldest loaded, the pages above. */
      older: () => {
        if (total && at < total - 1) setAt(at + 1);
        else if (further) void findMore();
      },
      newer: () => setAt((i) => Math.max(0, i - 1)),
    }),
    [open, query, total, at, settle, further, aboveFailed, seeking, findMore],
  );
}

const NONE: Hit[] = [];

export type ThreadSearch = ReturnType<typeof useThreadSearch>;

/** The search button in the header. */
export function SearchToggle({ search }: { search: ThreadSearch }) {
  return (
    <Toggle
      pressed={search.open}
      onPressedChange={(on) => (on ? search.show() : search.close())}
      aria-label={search.open ? "검색 닫기" : "대화 검색"}
      className="size-11 text-faint hover:bg-transparent hover:text-dim data-[state=on]:bg-transparent data-[state=on]:text-foreground mouse:size-8"
    >
      <Search aria-hidden className="size-[18px] mouse:size-4" />
    </Toggle>
  );
}

/** The row under the header while searching. */
export function SearchBar({ search }: { search: ThreadSearch }) {
  if (!search.open) return null;
  return <SearchRow search={search} />;
}

/**
 * What is being typed stays here, so a key press renders this row and not
 * the screen around it; the thread is searched once typing pauses.
 */
function SearchRow({ search }: { search: ThreadSearch }) {
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState("");
  const { setQuery } = search;
  useEffect(() => {
    input.current?.focus();
  }, []);
  useEffect(() => {
    const t = window.setTimeout(() => setQuery(text), SETTLE_MS);
    return () => window.clearTimeout(t);
  }, [text, setQuery]);

  const q = search.query.trim();
  // "없음" only of the whole conversation; of what is loaded, it says so.
  const count = !q
    ? ""
    : search.seeking
      ? "찾는 중…"
      : search.total === 0
        ? search.further
          ? "불러온 대화엔 없음"
          : "없음"
        : `${search.at + 1}/${search.total}${search.further ? "+" : ""}`;
  const offerMore = Boolean(q) && !search.seeking && search.further && (search.total === 0 || search.at >= search.total - 1);
  return (
    <div className="inset-x-safe shrink-0 border-t border-edge-soft" role="search">
      <div className="mx-auto flex h-14 w-full max-w-page items-center gap-1 px-4 sm:px-8">
        <InputGroup
          surface="well"
          // The caret is the focus here: the row is nothing but this field.
          className="h-10 min-w-0 flex-1 rounded-full border-transparent mouse:h-10 has-[[data-slot=input-group-control]:focus-visible]:border-transparent has-[[data-slot=input-group-control]:focus-visible]:ring-0"
        >
          <InputGroupAddon className="pl-3.5">
            <Search aria-hidden />
          </InputGroupAddon>
          <InputGroupInput
            ref={input}
            type="search"
            enterKeyHint="search"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing) return;
              if (e.key === "Escape") search.close();
              if (e.key === "Enter") {
                e.preventDefault();
                // Enter searches at once what is typed; once it has, it walks.
                if (text !== search.query) setQuery(text);
                else if (e.shiftKey) search.newer();
                else search.older();
              }
            }}
            placeholder="대화에서 찾기"
            aria-label="대화에서 찾을 말"
            className="pl-1.5 mouse:text-body [&::-webkit-search-cancel-button]:hidden"
          />
          <InputGroupAddon align="inline-end" className="pr-3.5">
            <span className="tnum text-aside font-normal text-faint" aria-live="polite">
              {count}
            </span>
          </InputGroupAddon>
        </InputGroup>
        {offerMore ? (
          <button
            type="button"
            onClick={() => void search.findMore()}
            className="tap-y shrink-0 rounded-full px-2 text-aside text-foreground hover:text-ink focus-ring"
          >
            {search.aboveFailed ? "다시 찾기" : "더 찾기"}
          </button>
        ) : null}
        <button
          type="button"
          onClick={search.older}
          disabled={search.seeking || (search.at >= search.total - 1 && !search.further)}
          aria-label="이전 결과 (더 오래된)"
          className="flex size-10 shrink-0 items-center justify-center rounded-full text-dim hover:text-foreground focus-ring disabled:opacity-30"
        >
          <ChevronUp aria-hidden className="size-[18px]" />
        </button>
        <button
          type="button"
          onClick={search.newer}
          disabled={search.at <= 0}
          aria-label="다음 결과 (더 최근)"
          className="flex size-10 shrink-0 items-center justify-center rounded-full text-dim hover:text-foreground focus-ring disabled:opacity-30"
        >
          <ChevronDown aria-hidden className="size-[18px]" />
        </button>
        <button
          type="button"
          onClick={search.close}
          aria-label="검색 닫기"
          className="-mr-2 flex size-10 shrink-0 items-center justify-center rounded-full text-faint hover:text-foreground focus-ring"
        >
          <X aria-hidden className="size-[18px]" />
        </button>
      </div>
    </div>
  );
}
